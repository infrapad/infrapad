package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
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
)

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

	parts := strings.Split(fields[1], ".")
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

func newProxy(upstream *url.URL) http.Handler {
	reverseProxy := httputil.NewSingleHostReverseProxy(upstream)

	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/oauth/healthz" {
			response.Header().Set("Content-Type", "text/plain; charset=utf-8")
			response.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(response, "ok\n")
			return
		}

		authorizationValues, authorizationPresent := valuesForHeader(request.Header, "Authorization")
		removeHeader(request.Header, "Authorization")
		removeHeader(request.Header, "X-Forwarded-User")
		removeHeader(request.Header, "X-Forwarded-Email")

		if authorizationPresent {
			if len(authorizationValues) != 1 {
				http.Error(response, "invalid authorization", http.StatusBadRequest)
				return
			}
			derivedIdentity, err := identityFromAuthorization(authorizationValues[0])
			if err != nil {
				http.Error(response, "invalid authorization", http.StatusBadRequest)
				return
			}
			request.Header.Set("X-Forwarded-User", derivedIdentity.username)
			if derivedIdentity.hasEmail {
				request.Header.Set("X-Forwarded-Email", derivedIdentity.email)
			}
		}

		reverseProxy.ServeHTTP(response, request)
	})
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
