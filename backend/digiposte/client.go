package digiposte

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/holyhope/digiposte-go-sdk/login"
	chrome "github.com/holyhope/digiposte-go-sdk/login/chrome"
	digiposte "github.com/holyhope/digiposte-go-sdk/v1"
	digiconfig "github.com/rclone/rclone/backend/digiposte/config"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/fshttp"
	"github.com/rclone/rclone/lib/oauthutil"
	"golang.org/x/oauth2"
	"golang.org/x/time/rate"
)

func getClient(ctx context.Context, name string, m configmap.Mapper) (*digiposte.Client, error) {
	httpClient := fshttp.NewClient(ctx)
	httpClient.Jar = &cookiesJar{mapper: m}

	httpClient.Transport = newRateLimitedTransport(
		httpClient.Transport,
		rate.NewLimiter(rate.Every(1*time.Second), 5),
	)

	loginMethod, err := chrome.New(
		chrome.WithURL(digiconfig.DocumentURL(m)),
		chrome.WithRefreshFrequency(1000*time.Millisecond),
		chrome.WithTimeout(3*time.Minute),
	)
	if err != nil {
		return nil, fmt.Errorf("new chrome login method: %w", err)
	}

	previousToken, err := oauthutil.GetToken(name, m)
	if err != nil {
		fs.Errorf(nil, "failed to get token: %v", err)
	}

	digiposteClient, err := digiposte.NewAuthenticatedClient(ctx, httpClient, &digiposte.Config{
		APIURL:      digiconfig.APIURL(m),
		DocumentURL: digiconfig.DocumentURL(m),
		LoginMethod: loginMethod,
		Credentials: &login.Credentials{
			Username:  digiconfig.Username(m),
			Password:  digiconfig.Password(m),
			OTPSecret: digiconfig.OTPSecret(m),
		},
		SessionListener: func(session *digiposte.Session) {
			if err := digiconfig.SetCookies(m, session.Cookies); err != nil {
				fs.Errorf(nil, "failed to save cookies: %v", err)
			}

			if err := oauthutil.PutToken(name, m, session.Token, false); err != nil {
				fs.Errorf(nil, "failed to save token: %v", err)
			}
		},
		PreviousSession: &digiposte.Session{
			Token:   previousToken,
			Cookies: digiconfig.Cookies(m),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("new authenticated Digiposte client: %w", err)
	}
	return digiposteClient, nil
}

// GetClient returns an authenticated http.Client for Digiposte.
func GetClient(ctx context.Context, name string, oauthConfig *oauth2.Config, m configmap.Mapper) (*http.Client, error) {
	client := fshttp.NewClient(ctx)
	client.Jar = &cookiesJar{mapper: m}

	client.Transport = &rateLimitedTransport{
		RoundTripper: client.Transport,
		rateLimiter:  rate.NewLimiter(rate.Every(1*time.Second), 5),
	}

	client, _, err := oauthutil.NewClientWithBaseClient(ctx, name, m, oauthConfig, client)
	if err != nil {
		return nil, fmt.Errorf("new oauth client: %w", err)
	}

	return client, nil
}

func newRateLimitedTransport(base http.RoundTripper, rl *rate.Limiter) http.RoundTripper {
	return &rateLimitedTransport{
		RoundTripper: base,
		rateLimiter:  rl,
	}
}

type rateLimitedTransport struct {
	http.RoundTripper

	rateLimiter *rate.Limiter
}

func (t *rateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.rateLimiter.Wait(req.Context()); err != nil {
		return nil, fmt.Errorf("rate limited: %w", err)
	}

	resp, err := t.RoundTripper.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("round trip: %w", err)
	}

	return resp, nil
}

// cookiesJar implements the http.CookieJar interface.
// It saves the document cookies in the configuration file.
// Only cookies from the document URL are saved, others are ignored.
type cookiesJar struct {
	mapper configmap.Mapper
}

func (c *cookiesJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	documentURL := digiconfig.DocumentURL(c.mapper)

	parsedURL, err := url.Parse(documentURL)
	if err != nil {
		fs.Errorf(nil, "failed to save %d cookies, error parsing the document URL %q: %v", len(cookies), documentURL, err)
		return
	}

	if u.Hostname() != parsedURL.Hostname() || u.Scheme != parsedURL.Scheme || u.Port() != parsedURL.Port() || strings.HasPrefix(u.Path, parsedURL.Path) {
		fs.Debugf(nil, "skipping saving %d cookies, the %q does not match the document URL", len(cookies), u.String())
		return
	}

	if err := digiconfig.SetCookies(c.mapper, cookies); err != nil {
		fs.Errorf(nil, "failed to save %d cookies: %v", len(cookies), err)
	}
}

func (c *cookiesJar) Cookies(u *url.URL) []*http.Cookie {
	return digiconfig.Cookies(c.mapper)
}
