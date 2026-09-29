package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultListenAddress = ":8080"
	defaultUpstreamURL   = "http://host.containers.internal:8088"
	defaultHealthURL     = "http://127.0.0.1:8080/oauth/healthz"
	sessionCookieName    = "infrapad_dummy_auth"
)

var authPageTemplate = template.Must(template.New("auth").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>InfraPad dummy authentication</title>
  <style>
    body { color: #202124; font: 16px/1.5 sans-serif; margin: 2rem auto; max-width: 42rem; padding: 0 1rem; }
    form, dl { display: grid; gap: .75rem; }
    input, textarea, button { box-sizing: border-box; font: inherit; padding: .5rem; width: 100%; }
    textarea { min-height: 7rem; overflow-wrap: anywhere; }
    .actions { display: flex; gap: .75rem; }
    .warning { background: #fff3cd; border: 1px solid #ffe69c; padding: .75rem; }
    dt { font-weight: bold; }
    dd { margin: 0; }
  </style>
</head>
<body>
  <main>
    <h1>InfraPad dummy authentication</h1>
    <p class="warning"><strong>Warning:</strong> Dummy authentication provides no security and is for local development only.</p>
    {{if .Authenticated}}
      <h2>Current session</h2>
      <dl>
        <div><dt>Username</dt><dd>{{.Username}}</dd></div>
        {{if .HasEmail}}<div><dt>Email</dt><dd>{{.Email}}</dd></div>{{end}}
      </dl>
      <label for="token">Bearer token</label>
      <textarea id="token" readonly>{{.Token}}</textarea>
      <form method="post" action="/auth/logout">
        <button type="submit">Log out</button>
      </form>
    {{else}}
      <form method="post" action="/auth/login">
        {{if .HasReturnTo}}<input type="hidden" name="returnTo" value="{{.ReturnTo}}">{{end}}
        <label for="username">Username</label>
        <input id="username" name="username" required>
        <label for="email">Email (optional)</label>
        <input id="email" name="email">
        <div class="actions">
          <button type="submit">Log in</button>
          <button type="submit" formaction="/auth/token">Get token</button>
        </div>
      </form>
    {{end}}
  </main>
</body>
</html>
`))

type authPageData struct {
	Authenticated bool
	Username      string
	Email         string
	HasEmail      bool
	Token         string
	ReturnTo      string
	HasReturnTo   bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "dummy-auth: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("expected a subcommand: proxy, token, or healthcheck")
	}

	switch args[0] {
	case "proxy":
		return runProxy(args[1:])
	case "token":
		return runToken(args[1:], os.Stdout)
	case "healthcheck":
		return runHealthcheck(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q; expected proxy, token, or healthcheck", args[0])
	}
}

func runProxy(args []string) error {
	flags := flag.NewFlagSet("proxy", flag.ContinueOnError)
	listenAddress := flags.String("listen", defaultListenAddress, "HTTP listen address")
	upstreamAddress := flags.String("upstream", defaultUpstreamURL, "InfraPad upstream URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("proxy does not accept positional arguments")
	}

	upstream, err := url.Parse(*upstreamAddress)
	if err != nil {
		return fmt.Errorf("parse upstream URL: %w", err)
	}
	if upstream.Scheme != "http" && upstream.Scheme != "https" {
		return fmt.Errorf("upstream URL must use http or https")
	}
	if upstream.Host == "" {
		return fmt.Errorf("upstream URL must include a host")
	}

	log.Printf("dummy authentication proxy listening on %s and forwarding to %s", *listenAddress, upstream)
	return http.ListenAndServe(*listenAddress, newProxy(upstream))
}

func runToken(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("token", flag.ContinueOnError)
	username := flags.String("username", "", "required preferred username")
	email := flags.String("email", "", "optional email address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("token does not accept positional arguments")
	}
	if *username == "" {
		return fmt.Errorf("--username is required and must not be empty")
	}

	emailSet := false
	flags.Visit(func(current *flag.Flag) {
		if current.Name == "email" {
			emailSet = true
		}
	})

	token, err := generateToken(*username, *email, emailSet)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, token)
	return err
}

func runHealthcheck(args []string) error {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	healthURL := flags.String("url", defaultHealthURL, "proxy health endpoint")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("healthcheck does not accept positional arguments")
	}

	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(*healthURL)
	if err != nil {
		return fmt.Errorf("request health endpoint: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}

func generateToken(username, email string, includeEmail bool) (string, error) {
	if username == "" {
		return "", errors.New("username is required and must not be empty")
	}

	header := struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}{
		Algorithm: "none",
		Type:      "JWT",
	}
	payload := struct {
		Username string  `json:"preferred_username"`
		Email    *string `json:"email,omitempty"`
	}{
		Username: username,
	}
	if includeEmail {
		payload.Email = &email
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("encode token header: %w", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode token payload: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(payloadJSON) + ".", nil
}

type identity struct {
	username string
	email    string
	hasEmail bool
}

func identityFromAuthorization(value string) (identity, error) {
	fields := strings.Fields(value)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return identity{}, errors.New("authorization must use the Bearer scheme with one token")
	}

	return identityFromToken(fields[1])
}

func identityFromToken(token string) (identity, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return identity{}, errors.New("bearer token must have three compact JWT segments")
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return identity{}, fmt.Errorf("decode bearer token payload: %w", err)
	}

	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return identity{}, fmt.Errorf("decode bearer token claims: %w", err)
	}
	if claims == nil {
		return identity{}, errors.New("bearer token payload must be a JSON object")
	}

	usernameClaim, ok := claims["preferred_username"]
	if !ok {
		return identity{}, errors.New("preferred_username claim is required")
	}
	var usernameValue any
	if err := json.Unmarshal(usernameClaim, &usernameValue); err != nil {
		return identity{}, errors.New("preferred_username claim must be a non-empty string")
	}
	username, ok := usernameValue.(string)
	if !ok || username == "" {
		return identity{}, errors.New("preferred_username claim must be a non-empty string")
	}

	result := identity{username: username}
	if emailClaim, ok := claims["email"]; ok {
		var emailValue any
		if err := json.Unmarshal(emailClaim, &emailValue); err != nil {
			return identity{}, errors.New("email claim must be a string")
		}
		result.email, ok = emailValue.(string)
		if !ok {
			return identity{}, errors.New("email claim must be a string")
		}
		result.hasEmail = true
	}
	return result, nil
}

// This CORS policy is only for the local dummy proxy, not production authorization.
func browserAPIPath(path string) bool {
	return path == "/ui/config" || path == "/v1" || strings.HasPrefix(path, "/v1/")
}

func clearCORS(headers http.Header) {
	for name := range headers {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			delete(headers, name)
		}
	}
}

func setBrowserAPICORS(headers http.Header) {
	clearCORS(headers)
	headers.Set("Access-Control-Allow-Origin", "*")
	headers.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	headers.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
}

func newProxy(upstream *url.URL) http.Handler {
	reverseProxy := httputil.NewSingleHostReverseProxy(upstream)
	browserProxy := *reverseProxy
	browserProxy.ModifyResponse = func(upstreamResponse *http.Response) error {
		// The upstream may already set CORS headers. Drop all of them, including
		// credentials/exposed headers, so only the proxy's policy is sent.
		clearCORS(upstreamResponse.Header)
		return nil
	}

	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/auth":
			handleAuthPage(response, request)
			return
		case "/auth/login":
			handleAuthLogin(response, request)
			return
		case "/auth/token":
			handleAuthToken(response, request)
			return
		case "/auth/logout":
			handleAuthLogout(response, request)
			return
		}

		if request.Method == http.MethodGet && request.URL.Path == "/oauth/healthz" {
			response.Header().Set("Content-Type", "text/plain; charset=utf-8")
			response.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(response, "ok\n")
			return
		}

		proxy := reverseProxy
		if browserAPIPath(request.URL.Path) {
			setBrowserAPICORS(response.Header()) // Also covers proxy-generated 400/502 errors.
			proxy = &browserProxy
			if request.Method == http.MethodOptions && request.Header.Get("Origin") != "" &&
				request.Header.Get("Access-Control-Request-Method") != "" {
				response.WriteHeader(http.StatusNoContent)
				return
			}
		}

		authorizationValues, authorizationPresent := valuesForHeader(request.Header, "Authorization")
		sessionCookie, sessionCookieErr := request.Cookie(sessionCookieName)
		removeHeader(request.Header, "Authorization")
		removeHeader(request.Header, "X-Forwarded-User")
		removeHeader(request.Header, "X-Forwarded-Email")
		removeCookie(request, sessionCookieName)

		var derivedIdentity identity
		var authenticated bool
		if authorizationPresent {
			if len(authorizationValues) != 1 {
				http.Error(response, "invalid authorization", http.StatusBadRequest)
				return
			}
			var err error
			derivedIdentity, err = identityFromAuthorization(authorizationValues[0])
			if err != nil {
				http.Error(response, "invalid authorization", http.StatusBadRequest)
				return
			}
			authenticated = true
		} else if sessionCookieErr == nil {
			var err error
			derivedIdentity, err = identityFromToken(sessionCookie.Value)
			if err != nil {
				expireSessionCookie(response)
			} else {
				authenticated = true
			}
		}

		if authenticated {
			request.Header.Set("X-Forwarded-User", derivedIdentity.username)
			if derivedIdentity.hasEmail {
				request.Header.Set("X-Forwarded-Email", derivedIdentity.email)
			}
		}

		proxy.ServeHTTP(response, request)
	})
}

func handleAuthPage(response http.ResponseWriter, request *http.Request) {
	setAuthResponseHeaders(response)
	if !allowMethod(response, request, http.MethodGet) {
		return
	}

	data := authPageData{}
	if returnTo := request.URL.Query().Get("returnTo"); isSafeReturnTo(returnTo) {
		data.ReturnTo = returnTo
		data.HasReturnTo = true
	}
	if sessionCookie, err := request.Cookie(sessionCookieName); err == nil {
		derivedIdentity, err := identityFromToken(sessionCookie.Value)
		if err != nil {
			expireSessionCookie(response)
		} else {
			data.Authenticated = true
			data.Username = derivedIdentity.username
			data.Email = derivedIdentity.email
			data.HasEmail = derivedIdentity.hasEmail
			data.Token = sessionCookie.Value
		}
	}

	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := authPageTemplate.Execute(response, data); err != nil {
		log.Printf("render authentication page: %v", err)
	}
}

func handleAuthLogin(response http.ResponseWriter, request *http.Request) {
	setAuthResponseHeaders(response)
	if !allowMethod(response, request, http.MethodPost) {
		return
	}

	username, email, includeEmail, ok := parseIdentityForm(response, request)
	if !ok {
		return
	}
	token, err := generateToken(username, email, includeEmail)
	if err != nil {
		http.Error(response, "invalid form data", http.StatusBadRequest)
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	redirectTarget := "/auth"
	if returnToValues := request.PostForm["returnTo"]; len(returnToValues) == 1 && isSafeReturnTo(returnToValues[0]) {
		redirectTarget = returnToValues[0]
	}
	http.Redirect(response, request, redirectTarget, http.StatusSeeOther)
}

func isSafeReturnTo(value string) bool {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.Contains(value, "\\") {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil &&
		!parsed.IsAbs() && parsed.Host == "" && parsed.Opaque == "" &&
		strings.HasPrefix(parsed.Path, "/") && !strings.HasPrefix(parsed.Path, "//") &&
		!strings.Contains(parsed.Path, "\\")
}

func handleAuthToken(response http.ResponseWriter, request *http.Request) {
	setAuthResponseHeaders(response)
	if !allowMethod(response, request, http.MethodPost) {
		return
	}

	username, email, includeEmail, ok := parseIdentityForm(response, request)
	if !ok {
		return
	}
	token, err := generateToken(username, email, includeEmail)
	if err != nil {
		http.Error(response, "invalid form data", http.StatusBadRequest)
		return
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprintln(response, token)
}

func handleAuthLogout(response http.ResponseWriter, request *http.Request) {
	setAuthResponseHeaders(response)
	if !allowMethod(response, request, http.MethodPost) {
		return
	}

	expireSessionCookie(response)
	http.Redirect(response, request, "/auth", http.StatusSeeOther)
}

func setAuthResponseHeaders(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
}

func allowMethod(response http.ResponseWriter, request *http.Request, allowed string) bool {
	if request.Method == allowed {
		return true
	}
	response.Header().Set("Allow", allowed)
	http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func parseIdentityForm(response http.ResponseWriter, request *http.Request) (string, string, bool, bool) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		http.Error(response, "invalid form data", http.StatusBadRequest)
		return "", "", false, false
	}
	if err := request.ParseForm(); err != nil {
		http.Error(response, "invalid form data", http.StatusBadRequest)
		return "", "", false, false
	}

	usernames := request.PostForm["username"]
	emails := request.PostForm["email"]
	if len(usernames) != 1 || usernames[0] == "" || len(emails) > 1 {
		http.Error(response, "invalid form data", http.StatusBadRequest)
		return "", "", false, false
	}
	if len(emails) == 0 || emails[0] == "" {
		return usernames[0], "", false, true
	}
	return usernames[0], emails[0], true, true
}

func expireSessionCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func removeCookie(request *http.Request, name string) {
	cookies := request.Cookies()
	removeHeader(request.Header, "Cookie")
	for _, cookie := range cookies {
		if cookie.Name != name {
			request.AddCookie(cookie)
		}
	}
}

func valuesForHeader(header http.Header, name string) ([]string, bool) {
	var values []string
	present := false
	for key, currentValues := range header {
		if strings.EqualFold(key, name) {
			present = true
			values = append(values, currentValues...)
		}
	}
	return values, present
}

func removeHeader(header http.Header, name string) {
	for key := range header {
		if strings.EqualFold(key, name) {
			delete(header, key)
		}
	}
}
