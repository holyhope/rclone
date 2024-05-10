// Package digiconfig contains the configuration keys and helpers for the Digiposte backend.
package digiconfig

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/holyhope/digiposte-go-sdk/settings"
	"github.com/rclone/rclone/fs/config/configmap"
)

const (
	APIURLKey      = "api_url"      // APIURLKey is the configuration key for API URL.
	DocumentURLKey = "document_url" // DocumentURLKey is the configuration key for document URL.
	UsernameKey    = "username"     // UsernameKey is the configuration key for username.
	PasswordKey    = "password"     // PasswordKey is the configuration key for password.
	OTPSecretKey   = "otp"          // OTPSecretKey is the configuration key for OTP secret.
	CookiesKey     = "cookies"      // CookiesKey is the configuration key for cookies.
)

var (
	MustReveal  = func(s string) string { return s } //nolint:gochecknoglobals
	MustObscure = func(s string) string { return s } //nolint:gochecknoglobals
)

// DocumentURL returns the document URL from the configuration.
func DocumentURL(m configmap.Getter) string {
	val, ok := m.Get(DocumentURLKey)
	if !ok {
		return settings.DefaultDocumentURL
	}

	return val
}

// APIURL returns the API URL from the configuration.
func APIURL(m configmap.Getter) string {
	val, ok := m.Get(APIURLKey)
	if !ok {
		return settings.DefaultAPIURL
	}

	return val
}

// Username returns the username from the configuration.
func Username(m configmap.Getter) string {
	val, _ := m.Get(UsernameKey)

	return val
}

// Password returns the password from the configuration.
func Password(m configmap.Getter) string {
	val, _ := m.Get(PasswordKey)

	return MustReveal(val)
}

// OTPSecret returns the OTP secret from the configuration.
func OTPSecret(m configmap.Getter) string {
	val, _ := m.Get(OTPSecretKey)

	return MustReveal(val)
}

//nolint:gochecknoglobals
var cookiesLock sync.RWMutex

// Cookies returns the cookies from the configuration.
func Cookies(m configmap.Getter) []*http.Cookie {
	cookiesLock.RLock()
	defer cookiesLock.RUnlock()

	val, ok := m.Get(CookiesKey)
	if !ok {
		return nil
	}

	var cypheredCookies []*http.Cookie
	if err := json.Unmarshal([]byte(val), &cypheredCookies); err != nil {
		panic(fmt.Errorf("unmarshal cookies: %w", err))
	}

	cookies := make([]*http.Cookie, 0, len(cypheredCookies))

	for _, cookie := range cypheredCookies {
		if err := cookie.Valid(); err != nil {
			panic(fmt.Errorf("invalid cookie %q: %w", cookie.Name, err))
		}

		cookie := *cookie
		cookie.Value = MustReveal(cookie.Value)
		cookies = append(cookies, &cookie)
	}

	return cookies
}

// SetCookies sets the cookies in the configuration.
func SetCookies(setter configmap.Setter, cookies []*http.Cookie) error {
	cookiesLock.Lock()
	defer cookiesLock.Unlock()

	cypheredCookies := make([]*http.Cookie, 0, len(cookies))

	for _, cookie := range cookies {
		cookie := *cookie
		cookie.Value = MustObscure(cookie.Value)
		cypheredCookies = append(cypheredCookies, &cookie)
	}

	cookiesBytes, err := json.Marshal(cypheredCookies)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	setter.Set(CookiesKey, string(cookiesBytes))

	return nil
}
