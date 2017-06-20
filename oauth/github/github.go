package github

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"

	jwt "github.com/dgrijalva/jwt-go"
	"github.com/google/go-github/github"
	"github.com/qor/auth"
	"github.com/qor/auth/auth_identity"
	"github.com/qor/qor/utils"
	"golang.org/x/oauth2"
)

var (
	AuthorizeURL = "https://github.com/login/oauth/authorize"
	TokenURL     = "https://github.com/login/oauth/access_token"
)

// githubProvider provide login with github method
type GithubProvider struct {
	*Config
}

// Config github Config
type Config struct {
	ClientID         string
	ClientSecret     string
	AuthorizeURL     string
	TokenURL         string
	RedirectURL      string
	Scopes           []string
	AuthorizeHandler func(request *http.Request, writer http.ResponseWriter, session *auth.Session) (interface{}, error)
}

func New(config *Config) *GithubProvider {
	if config == nil {
		config = &Config{}
	}

	provider := &GithubProvider{Config: config}

	if config.ClientID == "" {
		panic(errors.New("Github's ClientID can't be blank"))
	}

	if config.ClientSecret == "" {
		panic(errors.New("Github's ClientSecret can't be blank"))
	}

	if config.AuthorizeURL == "" {
		config.AuthorizeURL = AuthorizeURL
	}

	if config.TokenURL == "" {
		config.TokenURL = TokenURL
	}

	if config.AuthorizeHandler == nil {
		config.AuthorizeHandler = func(req *http.Request, writer http.ResponseWriter, session *auth.Session) (interface{}, error) {
			var (
				currentUser  interface{}
				authInfo     auth_identity.Basic
				tx           = session.Auth.GetDB(req)
				authIdentity = reflect.New(utils.ModelType(session.Auth.Config.AuthIdentityModel)).Interface()
			)

			state := req.URL.Query().Get("state")
			token, err := jwt.Parse(state, func(token *jwt.Token) (interface{}, error) {
				if token.Method != session.Auth.Config.SigningMethod {
					return nil, fmt.Errorf("unexpected signing method")
				}
				return []byte(session.Auth.Config.SignedString), nil
			})

			if claims, ok := token.Claims.(*jwt.StandardClaims); ok && (!token.Valid || claims.Subject != "state") {
				return nil, auth.ErrUnauthorized
			}

			if err == nil {
				oauthCfg := provider.OAuthConfig(req, session)
				tkn, err := oauthCfg.Exchange(oauth2.NoContext, req.URL.Query().Get("code"))

				if err != nil {
					return nil, err
				}

				client := github.NewClient(oauthCfg.Client(oauth2.NoContext, tkn))
				user, _, err := client.Users.Get("")
				if err != nil {
					return nil, err
				}

				authInfo.Provider = provider.GetName()
				authInfo.UID = fmt.Sprint(*user.ID)

				if !tx.Model(authIdentity).Where(authInfo).Scan(&authInfo).RecordNotFound() {
					if session.Auth.Config.UserModel != nil {
						if authInfo.UserID == "" {
							return nil, auth.ErrInvalidAccount
						}
						currentUser := reflect.New(utils.ModelType(session.Auth.Config.UserModel)).Interface()
						err := tx.First(currentUser, authInfo.UserID).Error
						return currentUser, err
					}
					return authInfo, nil
				}

				if session.Auth.Config.UserModel != nil {
					currentUser = reflect.New(utils.ModelType(session.Auth.Config.UserModel)).Interface()
					if err = tx.Create(currentUser).Error; err == nil {
						authInfo.UserID = fmt.Sprint(tx.NewScope(currentUser).PrimaryKeyValue())
					} else {
						return nil, err
					}
				} else {
					currentUser = authIdentity
				}

				err = tx.Where(authInfo).FirstOrCreate(authIdentity).Error
				return currentUser, err
			}

			return nil, err
		}
	}
	return provider
}

// GetName return provider name
func (GithubProvider) GetName() string {
	return "github"
}

// OAuthConfig return oauth config based on configuration
func (provider GithubProvider) OAuthConfig(req *http.Request, session *auth.Session) *oauth2.Config {
	var (
		config = provider.Config
		scheme = req.URL.Scheme
	)

	if scheme == "" {
		scheme = "http://"
	}

	return &oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  config.AuthorizeURL,
			TokenURL: config.TokenURL,
		},
		RedirectURL: scheme + req.Host + session.AuthURL("github/callback"),
		Scopes:      config.Scopes,
	}
}

// Login implemented login with github provider
func (provider GithubProvider) Login(req *http.Request, writer http.ResponseWriter, session *auth.Session) {
	token := jwt.NewWithClaims(session.Auth.Config.SigningMethod, jwt.StandardClaims{Subject: "state"})
	signedToken, _ := token.SignedString([]byte(session.Auth.Config.SignedString))

	url := provider.OAuthConfig(req, session).AuthCodeURL(signedToken)
	http.Redirect(writer, req, url, http.StatusFound)
}

// Logout implemented logout with github provider
func (GithubProvider) Logout(request *http.Request, writer http.ResponseWriter, session *auth.Session) {
}

// Register implemented register with github provider
func (provider GithubProvider) Register(request *http.Request, writer http.ResponseWriter, session *auth.Session) {
	provider.Login(request, writer, session)
}

// Callback implement Callback with github provider
func (provider GithubProvider) Callback(req *http.Request, writer http.ResponseWriter, session *auth.Session) {
	session.Auth.LoginHandler(req, writer, session, provider.AuthorizeHandler)
}

// ServeHTTP implement ServeHTTP with github provider
func (GithubProvider) ServeHTTP(*http.Request, http.ResponseWriter, *auth.Session) {
}
