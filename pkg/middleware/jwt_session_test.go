package middleware

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"time"

	"github.com/coreos/go-oidc"
	middlewareapi "github.com/oauth2-proxy/oauth2-proxy/pkg/apis/middleware"
	sessionsapi "github.com/oauth2-proxy/oauth2-proxy/pkg/apis/sessions"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
)

type noOpKeySet struct {
}

func (noOpKeySet) VerifySignature(ctx context.Context, jwt string) (payload []byte, err error) {
	splitStrings := strings.Split(jwt, ".")
	payloadString := splitStrings[1]
	return base64.RawURLEncoding.DecodeString(payloadString)
}

var _ = Describe("JWT Session Suite", func() {
	/* token payload:
	{
	  "sub": "1234567890",
	  "aud": "https://test.myapp.com",
	  "name": "John Doe",
	  "email": "john@example.com",
	  "iss": "https://issuer.example.com",
	  "iat": 1553691215,
	  "exp": 1912151821
	}
	*/
	const verifiedToken = "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJzdWIiOiIxMjM0NTY3ODkwIiwiYXVkIjoiaHR0cHM6Ly90ZXN0Lm15YXBwLmNvbSIsIm5hbWUiOiJKb2huIERvZSIsImVtY" +
		"WlsIjoiam9obkBleGFtcGxlLmNvbSIsImlzcyI6Imh0dHBzOi8vaXNzdWVyLmV4YW1wbGUuY29tIiwiaWF0IjoxNTUzNjkxMj" +
		"E1LCJleHAiOjE5MTIxNTE4MjF9." +
		"rLVyzOnEldUq_pNkfa-WiV8TVJYWyZCaM2Am_uo8FGg11zD7l-qmz3x1seTvqpH6Y0Ty00fmv6dJnGnC8WMnPXQiodRTfhBSe" +
		"OKZMu0HkMD2sg52zlKkbfLTO6ic5VnbVgwjjrB8am_Ta6w7kyFUaB5C1BsIrrLMldkWEhynbb8"

	const verifiedTokenXOAuthBasicBase64 = `ZXlKaGJHY2lPaUpTVXpJMU5pSXNJblI1Y0NJNklrcFhWQ0o5LmV5SnpkV0lpT2lJeE1qTTBOVFkz
T0Rrd0lpd2lZWFZrSWpvaWFIUjBjSE02THk5MFpYTjBMbTE1WVhCd0xtTnZiU0lzSW01aGJXVWlP
aUpLYjJodUlFUnZaU0lzSW1WdFlXbHNJam9pYW05b2JrQmxlR0Z0Y0d4bExtTnZiU0lzSW1semN5
STZJbWgwZEhCek9pOHZhWE56ZFdWeUxtVjRZVzF3YkdVdVkyOXRJaXdpYVdGMElqb3hOVFV6Tmpr
eE1qRTFMQ0psZUhBaU9qRTVNVEl4TlRFNE1qRjkuckxWeXpPbkVsZFVxX3BOa2ZhLVdpVjhUVkpZ
V3laQ2FNMkFtX3VvOEZHZzExekQ3bC1xbXozeDFzZVR2cXBINlkwVHkwMGZtdjZkSm5HbkM4V01u
UFhRaW9kUlRmaEJTZU9LWk11MEhrTUQyc2c1MnpsS2tiZkxUTzZpYzVWbmJWZ3dqanJCOGFtX1Rh
Nnc3a3lGVWFCNUMxQnNJcnJMTWxka1dFaHluYmI4Ongtb2F1dGgtYmFzaWM=`

	var verifiedSessionExpiry = time.Unix(1912151821, 0)
	var verifiedSession = &sessionsapi.SessionState{
		AccessToken: verifiedToken,
		IDToken:     verifiedToken,
		Email:       "john@example.com",
		User:        "1234567890",
		ExpiresOn:   &verifiedSessionExpiry,
	}

	// validToken will pass the token regex so can be used to check token fetching
	// is valid. It will not pass the OIDC Verifier however.
	const validToken = "eyJfoobar.eyJfoobar.12345asdf"

	Context("JwtSessionLoader", func() {
		var verifier *oidc.IDTokenVerifier
		const nonVerifiedToken = validToken

		BeforeEach(func() {
			keyset := noOpKeySet{}
			verifier = oidc.NewVerifier("https://issuer.example.com", keyset,
				&oidc.Config{ClientID: "https://test.myapp.com", SkipExpiryCheck: true})
		})

		type jwtSessionLoaderTableInput struct {
			authorizationHeader string
			existingSession     *sessionsapi.SessionState
			expectedSession     *sessionsapi.SessionState
		}

		DescribeTable("with an authorization header",
			func(in jwtSessionLoaderTableInput) {
				scope := &middlewareapi.RequestScope{
					Session: in.existingSession,
				}

				// Set up the request with the authorization header and a request scope
				req := httptest.NewRequest("", "/", nil)
				req.Header.Set("Authorization", in.authorizationHeader)
				contextWithScope := context.WithValue(req.Context(), requestScopeKey, scope)
				req = req.WithContext(contextWithScope)

				rw := httptest.NewRecorder()

				sessionLoaders := []middlewareapi.TokenToSessionLoader{
					{
						Verifier: verifier,
					},
				}

				// Create the handler with a next handler that will capture the session
				// from the scope
				var gotSession *sessionsapi.SessionState
				handler := NewJwtSessionLoader(sessionLoaders)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotSession = r.Context().Value(requestScopeKey).(*middlewareapi.RequestScope).Session
				}))
				handler.ServeHTTP(rw, req)

				Expect(gotSession).To(Equal(in.expectedSession))
			},
			Entry("<no value>", jwtSessionLoaderTableInput{
				authorizationHeader: "",
				existingSession:     nil,
				expectedSession:     nil,
			}),
			Entry("abcdef", jwtSessionLoaderTableInput{
				authorizationHeader: "abcdef",
				existingSession:     nil,
				expectedSession:     nil,
			}),
			Entry("abcdef  (with existing session)", jwtSessionLoaderTableInput{
				authorizationHeader: "abcdef",
				existingSession:     &sessionsapi.SessionState{User: "user"},
				expectedSession:     &sessionsapi.SessionState{User: "user"},
			}),
			Entry("Bearer <verifiedToken>", jwtSessionLoaderTableInput{
				authorizationHeader: fmt.Sprintf("Bearer %s", verifiedToken),
				existingSession:     nil,
				expectedSession:     verifiedSession,
			}),
			Entry("Bearer <nonVerifiedToken>", jwtSessionLoaderTableInput{
				authorizationHeader: fmt.Sprintf("Bearer %s", nonVerifiedToken),
				existingSession:     nil,
				expectedSession:     nil,
			}),
			Entry("Bearer <verifiedToken> (with existing session)", jwtSessionLoaderTableInput{
				authorizationHeader: fmt.Sprintf("Bearer %s", verifiedToken),
				existingSession:     &sessionsapi.SessionState{User: "user"},
				expectedSession:     &sessionsapi.SessionState{User: "user"},
			}),
			Entry("Basic Base64(<nonVerifiedToken>:) (No password)", jwtSessionLoaderTableInput{
				authorizationHeader: "Basic ZXlKZm9vYmFyLmV5SmZvb2Jhci4xMjM0NWFzZGY6",
				existingSession:     nil,
				expectedSession:     nil,
			}),
			Entry("Basic Base64(<verifiedToken>:x-oauth-basic) (Sentinel password)", jwtSessionLoaderTableInput{
				authorizationHeader: fmt.Sprintf("Basic %s", verifiedTokenXOAuthBasicBase64),
				existingSession:     nil,
				expectedSession:     verifiedSession,
			}),
		)

	})

	Context("getJWTSession", func() {
		var j *jwtSessionLoader
		const nonVerifiedToken = validToken

		BeforeEach(func() {
			keyset := noOpKeySet{}
			verifier := oidc.NewVerifier("https://issuer.example.com", keyset,
				&oidc.Config{ClientID: "https://test.myapp.com", SkipExpiryCheck: true})

			j = &jwtSessionLoader{
				jwtRegex: regexp.MustCompile(jwtRegexFormat),
				sessionLoaders: []middlewareapi.TokenToSessionLoader{
					{
						Verifier:       verifier,
						TokenToSession: createSessionStateFromBearerToken,
					},
				},
			}
		})

		type getJWTSessionTableInput struct {
			authorizationHeader string
			expectedErr         error
			expectedSession     *sessionsapi.SessionState
		}

		DescribeTable("with an authorization header",
			func(in getJWTSessionTableInput) {
				req := httptest.NewRequest("", "/", nil)
				req.Header.Set("Authorization", in.authorizationHeader)

				session, err := j.getJwtSession(req)
				if in.expectedErr != nil {
					Expect(err).To(MatchError(in.expectedErr))
				} else {
					Expect(err).ToNot(HaveOccurred())
				}
				Expect(session).To(Equal(in.expectedSession))
			},
			Entry("<no value>", getJWTSessionTableInput{
				authorizationHeader: "",
				expectedErr:         nil,
				expectedSession:     nil,
			}),
			Entry("abcdef", getJWTSessionTableInput{
				authorizationHeader: "abcdef",
				expectedErr:         errors.New("invalid authorization header: \"abcdef\""),
				expectedSession:     nil,
			}),
			Entry("Bearer abcdef", getJWTSessionTableInput{
				authorizationHeader: "Bearer abcdef",
				expectedErr:         errors.New("no valid bearer token found in authorization header"),
				expectedSession:     nil,
			}),
			Entry("Bearer <nonVerifiedToken>", getJWTSessionTableInput{
				authorizationHeader: fmt.Sprintf("Bearer %s", nonVerifiedToken),
				expectedErr:         errors.New("unable to verify jwt token: \"Bearer eyJfoobar.eyJfoobar.12345asdf\""),
				expectedSession:     nil,
			}),
			Entry("Bearer <verifiedToken>", getJWTSessionTableInput{
				authorizationHeader: fmt.Sprintf("Bearer %s", verifiedToken),
				expectedErr:         nil,
				expectedSession:     verifiedSession,
			}),
			Entry("Basic Base64(<nonVerifiedToken>:) (No password)", getJWTSessionTableInput{
				authorizationHeader: "Basic ZXlKZm9vYmFyLmV5SmZvb2Jhci4xMjM0NWFzZGY6",
				expectedErr:         errors.New("unable to verify jwt token: \"Basic ZXlKZm9vYmFyLmV5SmZvb2Jhci4xMjM0NWFzZGY6\""),
				expectedSession:     nil,
			}),
			Entry("Basic Base64(<verifiedToken>:x-oauth-basic) (Sentinel password)", getJWTSessionTableInput{
				authorizationHeader: fmt.Sprintf("Basic %s", verifiedTokenXOAuthBasicBase64),
				expectedErr:         nil,
				expectedSession:     verifiedSession,
			}),
		)
	})

	Context("findBearerTokenFromHeader", func() {
		var j *jwtSessionLoader

		BeforeEach(func() {
			j = &jwtSessionLoader{
				jwtRegex: regexp.MustCompile(jwtRegexFormat),
			}
		})

		type findBearerTokenFromHeaderTableInput struct {
			header        string
			expectedErr   error
			expectedToken string
		}

		DescribeTable("with a header",
			func(in findBearerTokenFromHeaderTableInput) {
				token, err := j.findBearerTokenFromHeader(in.header)
				if in.expectedErr != nil {
					Expect(err).To(MatchError(in.expectedErr))
				} else {
					Expect(err).ToNot(HaveOccurred())
				}
				Expect(token).To(Equal(in.expectedToken))
			},
			Entry("Bearer", findBearerTokenFromHeaderTableInput{
				header:        "Bearer",
				expectedErr:   errors.New("invalid authorization header: \"Bearer\""),
				expectedToken: "",
			}),
			Entry("Bearer abc def", findBearerTokenFromHeaderTableInput{
				header:        "Bearer abc def",
				expectedErr:   errors.New("invalid authorization header: \"Bearer abc def\""),
				expectedToken: "",
			}),
			Entry("Bearer abcdef", findBearerTokenFromHeaderTableInput{
				header:        "Bearer abcdef",
				expectedErr:   errors.New("no valid bearer token found in authorization header"),
				expectedToken: "",
			}),
			Entry("Bearer <valid-token>", findBearerTokenFromHeaderTableInput{
				header:        fmt.Sprintf("Bearer %s", validToken),
				expectedErr:   nil,
				expectedToken: validToken,
			}),
			Entry("Basic invalid-base64", findBearerTokenFromHeaderTableInput{
				header:        "Basic invalid-base64",
				expectedErr:   errors.New("invalid basic auth token: illegal base64 data at input byte 7"),
				expectedToken: "",
			}),
			Entry("Basic Base64(<validToken>:) (No password)", findBearerTokenFromHeaderTableInput{
				header:        "Basic ZXlKZm9vYmFyLmV5SmZvb2Jhci4xMjM0NWFzZGY6",
				expectedErr:   nil,
				expectedToken: validToken,
			}),
			Entry("Basic Base64(<validToken>:x-oauth-basic) (Sentinel password)", findBearerTokenFromHeaderTableInput{
				header:        "Basic ZXlKZm9vYmFyLmV5SmZvb2Jhci4xMjM0NWFzZGY6eC1vYXV0aC1iYXNpYw==",
				expectedErr:   nil,
				expectedToken: validToken,
			}),
			Entry("Basic Base64(any-user:<validToken>) (Matching password)", findBearerTokenFromHeaderTableInput{
				header:        "Basic YW55LXVzZXI6ZXlKZm9vYmFyLmV5SmZvb2Jhci4xMjM0NWFzZGY=",
				expectedErr:   nil,
				expectedToken: validToken,
			}),
			Entry("Basic Base64(any-user:any-password) (No matches)", findBearerTokenFromHeaderTableInput{
				header:        "Basic YW55LXVzZXI6YW55LXBhc3N3b3Jk",
				expectedErr:   errors.New("invalid basic auth token found in authorization header"),
				expectedToken: "",
			}),
			Entry("Basic Base64(any-user any-password) (Invalid format)", findBearerTokenFromHeaderTableInput{
				header:        "Basic YW55LXVzZXIgYW55LXBhc3N3b3Jk",
				expectedErr:   errors.New("invalid format: \"any-user any-password\""),
				expectedToken: "",
			}),
			Entry("Something <valid-token>", findBearerTokenFromHeaderTableInput{
				header:        fmt.Sprintf("Something %s", validToken),
				expectedErr:   errors.New("no valid bearer token found in authorization header"),
				expectedToken: "",
			}),
		)

	})

	Context("getBasicToken", func() {
		var j *jwtSessionLoader

		BeforeEach(func() {
			j = &jwtSessionLoader{
				jwtRegex: regexp.MustCompile(jwtRegexFormat),
			}
		})

		type getBasicTokenTableInput struct {
			token         string
			expectedErr   error
			expectedToken string
		}

		DescribeTable("with a token",
			func(in getBasicTokenTableInput) {
				token, err := j.getBasicToken(in.token)
				if in.expectedErr != nil {
					Expect(err).To(MatchError(in.expectedErr))
				} else {
					Expect(err).ToNot(HaveOccurred())
				}
				Expect(token).To(Equal(in.expectedToken))
			},
			Entry("invalid-base64", getBasicTokenTableInput{
				token:         "invalid-base64",
				expectedErr:   errors.New("invalid basic auth token: illegal base64 data at input byte 7"),
				expectedToken: "",
			}),
			Entry("Base64(<validToken>:) (No password)", getBasicTokenTableInput{
				token:         "ZXlKZm9vYmFyLmV5SmZvb2Jhci4xMjM0NWFzZGY6",
				expectedErr:   nil,
				expectedToken: validToken,
			}),
			Entry("Base64(<validToken>:x-oauth-basic) (Sentinel password)", getBasicTokenTableInput{
				token:         "ZXlKZm9vYmFyLmV5SmZvb2Jhci4xMjM0NWFzZGY6eC1vYXV0aC1iYXNpYw==",
				expectedErr:   nil,
				expectedToken: validToken,
			}),
			Entry("Base64(any-user:<validToken>) (Matching password)", getBasicTokenTableInput{
				token:         "YW55LXVzZXI6ZXlKZm9vYmFyLmV5SmZvb2Jhci4xMjM0NWFzZGY=",
				expectedErr:   nil,
				expectedToken: validToken,
			}),
			Entry("Base64(any-user:any-password) (No matches)", getBasicTokenTableInput{
				token:         "YW55LXVzZXI6YW55LXBhc3N3b3Jk",
				expectedErr:   errors.New("invalid basic auth token found in authorization header"),
				expectedToken: "",
			}),
			Entry("Base64(any-user any-password) (Invalid format)", getBasicTokenTableInput{
				token:         "YW55LXVzZXIgYW55LXBhc3N3b3Jk",
				expectedErr:   errors.New("invalid format: \"any-user any-password\""),
				expectedToken: "",
			}),
		)
	})
})
