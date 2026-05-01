package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo-contrib/session"
	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
	"github.com/rs/xid"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/ngoduykhanh/wireguard-ui/model"
	"github.com/ngoduykhanh/wireguard-ui/store"
	"github.com/ngoduykhanh/wireguard-ui/util"
)

type webAuthnUser struct {
	u model.User
}

var (
	passkeyRegMu       sync.Mutex
	passkeyRegSessions = map[string]webauthn.SessionData{}
	passkeyLoginMu       sync.Mutex
	passkeyLoginSessions = map[string]webauthn.SessionData{}
)

// ClearStoredPasskeysForAllUsers removes WebAuthn credentials from every user (e.g. after disabling Passkeys in settings).
func ClearStoredPasskeysForAllUsers(db store.IStore) error {
	if db == nil {
		return nil
	}
	users, err := db.GetUsers()
	if err != nil {
		return err
	}
	for _, u := range users {
		if len(u.Passkeys) == 0 {
			continue
		}
		u.Passkeys = nil
		if err := db.SaveUser(u); err != nil {
			return fmt.Errorf("%s: %w", u.Username, err)
		}
	}
	return nil
}

func (w webAuthnUser) WebAuthnID() []byte {
	return []byte(w.u.Username)
}
func (w webAuthnUser) WebAuthnName() string {
	return w.u.Username
}
func (w webAuthnUser) WebAuthnDisplayName() string {
	if s := strings.TrimSpace(w.u.DisplayName); s != "" {
		return s
	}
	return w.u.Username
}
func (w webAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return w.u.Passkeys
}
func (w webAuthnUser) WebAuthnIcon() string { return "" }

func webauthnForRequest(c echo.Context) (*webauthn.WebAuthn, error) {
	host := c.Request().Header.Get("X-Forwarded-Host")
	if host == "" {
		host = c.Request().Host
	}
	if strings.Contains(host, ",") {
		host = strings.TrimSpace(strings.Split(host, ",")[0])
	}
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	if host == "" {
		host = "localhost"
	}

	reqHost := c.Request().Header.Get("X-Forwarded-Host")
	if reqHost == "" {
		reqHost = c.Request().Host
	}
	if strings.Contains(reqHost, ",") {
		reqHost = strings.TrimSpace(strings.Split(reqHost, ",")[0])
	}
	proto := c.Request().Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if c.Request().TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	origin := proto + "://" + reqHost

	rpID := util.LookupEnvOrString(util.WebAuthnRPIDEnvVar, host)
	rpDisplayName := util.LookupEnvOrString(util.WebAuthnRPDisplayNameEnvVar, "WireGuard UI")
	rpOrigins := util.LookupEnvOrStrings(util.WebAuthnRPOriginsEnvVar, []string{origin})
	if len(rpOrigins) == 0 {
		rpOrigins = []string{origin}
	}
	return webauthn.New(&webauthn.Config{
		RPDisplayName: rpDisplayName,
		RPID:          rpID,
		RPOrigins:     rpOrigins,
	})
}

func setLoginSession(c echo.Context, dbuser model.User, rememberMe bool, sessionTimeoutMinutes int) error {
	ageMax := 0
	if sessionTimeoutMinutes > 0 {
		ageMax = sessionTimeoutMinutes * 60
	} else if rememberMe {
		// Only extend cookie max-age when global settings do not enforce a finite session idle timeout.
		rememberAge := 86400 * 7
		if ageMax < rememberAge {
			ageMax = rememberAge
		}
	}
	if ageMax <= 0 {
		ageMax = 86400
	}

	cookiePath := util.GetCookiePath()
	sess, _ := session.Get("session", c)
	sess.Options = &sessions.Options{
		Path:     cookiePath,
		MaxAge:   ageMax,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}

	tokenUID := xid.New().String()
	now := time.Now().UTC().Unix()
	userCRC := util.GetDBUserCRC32(dbuser)
	util.DBUsersToCRC32[dbuser.Username] = userCRC

	delete(sess.Values, sessionPkLoginCredKey)
	sess.Values["username"] = dbuser.Username
	sess.Values["user_hash"] = userCRC
	sess.Values["admin"] = dbuser.Admin
	sess.Values["session_token"] = tokenUID
	sess.Values["max_age"] = ageMax
	sess.Values["created_at"] = now
	sess.Values["updated_at"] = now
	if err := sess.Save(c.Request(), c.Response()); err != nil {
		return err
	}

	cookie := new(http.Cookie)
	cookie.Name = "session_token"
	cookie.Path = cookiePath
	cookie.Value = tokenUID
	cookie.MaxAge = ageMax
	cookie.HttpOnly = true
	cookie.SameSite = http.SameSiteLaxMode
	c.SetCookie(cookie)
	return nil
}

func persistSessionPasskeyLoginCredential(c echo.Context, cred *webauthn.Credential) {
	if cred == nil || len(cred.ID) == 0 {
		return
	}
	sess, _ := session.Get("session", c)
	sess.Values[sessionPkLoginCredKey] = base64.RawURLEncoding.EncodeToString(cred.ID)
	_ = sess.Save(c.Request(), c.Response())
}

func PasskeyBeginRegister(db store.IStore) echo.HandlerFunc {
	return func(c echo.Context) error {
		globalSettings, _ := db.GetGlobalSettings()
		if !globalSettings.TOTPEnabled {
			return c.JSON(http.StatusForbidden, jsonHTTPResponse{false, "Passkeys are disabled in settings"})
		}
		username := c.Param("username")
		if username == "" {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Missing username"})
		}
		if !isAdmin(c) && username != currentUser(c) {
			return c.JSON(http.StatusForbidden, jsonHTTPResponse{false, "Not allowed"})
		}
		u, err := db.GetUserByName(username)
		if err != nil {
			return c.JSON(http.StatusNotFound, jsonHTTPResponse{false, "User not found"})
		}
		wa, err := webauthnForRequest(c)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, err.Error()})
		}
		// Prefer resident (discoverable) credentials so login without username works on supported authenticators.
		opts, sessionData, err := wa.BeginRegistration(webAuthnUser{u}, webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred))
		if err != nil {
			return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, err.Error()})
		}
		sess, _ := session.Get("session", c)
		token := xid.New().String()
		passkeyRegMu.Lock()
		passkeyRegSessions[token] = *sessionData
		passkeyRegMu.Unlock()
		sess.Values["passkey_reg_"+username] = token
		_ = sess.Save(c.Request(), c.Response())
		return c.JSON(http.StatusOK, opts)
	}
}

func PasskeyFinishRegister(db store.IStore) echo.HandlerFunc {
	return func(c echo.Context) error {
		globalSettings, _ := db.GetGlobalSettings()
		if !globalSettings.TOTPEnabled {
			return c.JSON(http.StatusForbidden, jsonHTTPResponse{false, "Passkeys are disabled in settings"})
		}
		username := c.Param("username")
		if username == "" {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Missing username"})
		}
		if !isAdmin(c) && username != currentUser(c) {
			return c.JSON(http.StatusForbidden, jsonHTTPResponse{false, "Not allowed"})
		}
		u, err := db.GetUserByName(username)
		if err != nil {
			return c.JSON(http.StatusNotFound, jsonHTTPResponse{false, "User not found"})
		}
		bodyBytes, errRead := io.ReadAll(c.Request().Body)
		if errRead != nil {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Cannot read request body"})
		}
		var raw map[string]interface{}
		credentialName := ""
		if json.Unmarshal(bodyBytes, &raw) == nil && raw != nil {
			if v, ok := raw["credential_name"].(string); ok {
				credentialName = strings.TrimSpace(v)
			}
			delete(raw, "credential_name")
			if clean, errM := json.Marshal(raw); errM == nil {
				bodyBytes = clean
			}
		}
		c.Request().Body = io.NopCloser(bytes.NewReader(bodyBytes))

		wa, err := webauthnForRequest(c)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, err.Error()})
		}
		sess, _ := session.Get("session", c)
		token, _ := sess.Values["passkey_reg_"+username].(string)
		if token == "" {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Registration session not found"})
		}
		passkeyRegMu.Lock()
		sessionData, ok := passkeyRegSessions[token]
		passkeyRegMu.Unlock()
		if !ok {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Registration session expired"})
		}
		cred, err := wa.FinishRegistration(webAuthnUser{u}, sessionData, c.Request())
		if err != nil {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, err.Error()})
		}
		already := false
		for _, ex := range u.Passkeys {
			if len(ex.ID) > 0 && bytes.Equal(ex.ID, cred.ID) {
				already = true
				break
			}
		}
		if !already {
			u.Passkeys = append(u.Passkeys, *cred)
			idKey := base64.RawURLEncoding.EncodeToString(cred.ID)
			if credentialName != "" {
				if u.PasskeyLabels == nil {
					u.PasskeyLabels = map[string]string{}
				}
				u.PasskeyLabels[idKey] = credentialName
			}
			if err := db.SaveUser(u); err != nil {
				return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, err.Error()})
			}
			if username == currentUser(c) {
				// Keep session hash in the same session instance; a second sess.Save() happens below.
				sess.Values["username"] = u.Username
				sess.Values["admin"] = u.Admin
				sess.Values["user_hash"] = util.GetDBUserCRC32(u)
			}
		}
		delete(sess.Values, "passkey_reg_"+username)
		passkeyRegMu.Lock()
		delete(passkeyRegSessions, token)
		passkeyRegMu.Unlock()
		_ = sess.Save(c.Request(), c.Response())
		return c.JSON(http.StatusOK, jsonHTTPResponse{true, "Passkey registered"})
	}
}

// PasskeyRemove deletes one stored credential by id (base64url).
func PasskeyRemove(db store.IStore) echo.HandlerFunc {
	return func(c echo.Context) error {
		globalSettings, _ := db.GetGlobalSettings()
		if !globalSettings.TOTPEnabled {
			return c.JSON(http.StatusForbidden, jsonHTTPResponse{false, "Passkeys are disabled in settings"})
		}
		var body struct {
			Username     string `json:"username"`
			CredentialID string `json:"credential_id"`
		}
		if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Bad post data"})
		}
		un := strings.TrimSpace(body.Username)
		if un == "" || !usernameRegexp.MatchString(un) {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Invalid username"})
		}
		if !isAdmin(c) && un != currentUser(c) {
			return c.JSON(http.StatusForbidden, jsonHTTPResponse{false, "Not allowed"})
		}
		wantID, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(body.CredentialID))
		if err != nil || len(wantID) == 0 {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Invalid credential id"})
		}
		idKey := base64.RawURLEncoding.EncodeToString(wantID)
		u, err := db.GetUserByName(un)
		if err != nil {
			return c.JSON(http.StatusNotFound, jsonHTTPResponse{false, "User not found"})
		}
		kept := u.Passkeys[:0]
		for _, pc := range u.Passkeys {
			if len(pc.ID) > 0 && bytes.Equal(pc.ID, wantID) {
				continue
			}
			kept = append(kept, pc)
		}
		if len(kept) == len(u.Passkeys) {
			return c.JSON(http.StatusNotFound, jsonHTTPResponse{false, "Credential not found"})
		}
		u.Passkeys = kept
		if u.PasskeyLabels != nil {
			delete(u.PasskeyLabels, idKey)
		}
		// Any passkey removal revokes existing sessions for that account.
		u.AuthEpoch++
		if err := db.SaveUser(u); err != nil {
			return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, err.Error()})
		}
		if un == currentUser(c) {
			clearSession(c)
			return c.JSON(http.StatusOK, jsonHTTPReauthenticate{
				Status:         true,
				Message:        "Passkey eliminada. Vuelve a iniciar sesión.",
				Reauthenticate: true,
			})
		}
		return c.JSON(http.StatusOK, jsonHTTPResponse{Status: true, Message: "Passkey eliminada; sesiones del usuario revocadas"})
	}
}

// PasskeyRename updates the friendly name for an existing credential.
func PasskeyRename(db store.IStore) echo.HandlerFunc {
	return func(c echo.Context) error {
		globalSettings, _ := db.GetGlobalSettings()
		if !globalSettings.TOTPEnabled {
			return c.JSON(http.StatusForbidden, jsonHTTPResponse{false, "Passkeys are disabled in settings"})
		}
		var body struct {
			Username     string `json:"username"`
			CredentialID string `json:"credential_id"`
			Name         string `json:"name"`
		}
		if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Bad post data"})
		}
		un := strings.TrimSpace(body.Username)
		if un == "" || !usernameRegexp.MatchString(un) {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Invalid username"})
		}
		if !isAdmin(c) && un != currentUser(c) {
			return c.JSON(http.StatusForbidden, jsonHTTPResponse{false, "Not allowed"})
		}
		wantID, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(body.CredentialID))
		if err != nil || len(wantID) == 0 {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Invalid credential id"})
		}
		u, err := db.GetUserByName(un)
		if err != nil {
			return c.JSON(http.StatusNotFound, jsonHTTPResponse{false, "User not found"})
		}
		found := false
		for _, pc := range u.Passkeys {
			if len(pc.ID) > 0 && bytes.Equal(pc.ID, wantID) {
				found = true
				break
			}
		}
		if !found {
			return c.JSON(http.StatusNotFound, jsonHTTPResponse{false, "Credential not found"})
		}
		idKey := base64.RawURLEncoding.EncodeToString(wantID)
		if u.PasskeyLabels == nil {
			u.PasskeyLabels = map[string]string{}
		}
		u.PasskeyLabels[idKey] = strings.TrimSpace(body.Name)
		if err := db.SaveUser(u); err != nil {
			return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, err.Error()})
		}
		if un == currentUser(c) {
			setUser(c, u.Username, u.Admin, util.GetDBUserCRC32(u))
		}
		return c.JSON(http.StatusOK, jsonHTTPResponse{true, "Passkey renamed"})
	}
}

const passkeyDiscoverableSessionKey = "passkey_login_discoverable"

func discoverableUserLookup(db store.IStore) func(rawID, userHandle []byte) (webauthn.User, error) {
	return func(rawID, userHandle []byte) (webauthn.User, error) {
		users, err := db.GetUsers()
		if err != nil {
			return nil, err
		}
		for _, mu := range users {
			for _, pc := range mu.Passkeys {
				if len(pc.ID) > 0 && bytes.Equal(pc.ID, rawID) {
					return webAuthnUser{mu}, nil
				}
			}
		}
		return nil, fmt.Errorf("credential not found")
	}
}

func PasskeyBeginLogin(db store.IStore) echo.HandlerFunc {
	return func(c echo.Context) error {
		globalSettings, _ := db.GetGlobalSettings()
		if !globalSettings.TOTPEnabled {
			return c.JSON(http.StatusForbidden, jsonHTTPResponse{false, "Passkeys are disabled"})
		}
		data := make(map[string]interface{})
		if err := c.Bind(&data); err != nil {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Bad post data"})
		}
		username, _ := data["username"].(string)
		username = strings.TrimSpace(username)

		wa, err := webauthnForRequest(c)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, err.Error()})
		}
		sess, _ := session.Get("session", c)
		token := xid.New().String()

		if username == "" {
			// Discoverable / passkey-only login (no username).
			opts, sessionData, err := wa.BeginDiscoverableLogin()
			if err != nil {
				return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, err.Error()})
			}
			passkeyLoginMu.Lock()
			passkeyLoginSessions[token] = *sessionData
			passkeyLoginMu.Unlock()
			sess.Values[passkeyDiscoverableSessionKey] = token
			_ = sess.Save(c.Request(), c.Response())
			return c.JSON(http.StatusOK, map[string]interface{}{"status": true, "discoverable": true, "options": opts})
		}

		if !usernameRegexp.MatchString(username) {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Please provide a valid username"})
		}
		u, err := db.GetUserByName(username)
		if err != nil {
			return c.JSON(http.StatusUnauthorized, jsonHTTPResponse{false, "Invalid credentials"})
		}
		if len(u.Passkeys) == 0 {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "No passkey registered for this user"})
		}
		opts, sessionData, err := wa.BeginLogin(webAuthnUser{u})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, err.Error()})
		}
		passkeyLoginMu.Lock()
		passkeyLoginSessions[token] = *sessionData
		passkeyLoginMu.Unlock()
		sess.Values["passkey_login_"+username] = token
		_ = sess.Save(c.Request(), c.Response())
		return c.JSON(http.StatusOK, map[string]interface{}{"status": true, "discoverable": false, "username": username, "options": opts})
	}
}

func PasskeyFinishLogin(db store.IStore) echo.HandlerFunc {
	return func(c echo.Context) error {
		globalSettings, _ := db.GetGlobalSettings()
		if !globalSettings.TOTPEnabled {
			return c.JSON(http.StatusForbidden, jsonHTTPResponse{false, "Passkeys are disabled"})
		}
		// Read body for username/rememberMe, then restore it: c.Bind() would drain the body and
		// wa.FinishLogin would get an empty request → "Parse error for Assertion".
		bodyBytes, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Bad post data"})
		}
		c.Request().Body = io.NopCloser(bytes.NewReader(bodyBytes))
		var loginMeta struct {
			Username   string `json:"username"`
			RememberMe bool   `json:"rememberMe"`
		}
		if err := json.Unmarshal(bodyBytes, &loginMeta); err != nil {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Bad post data"})
		}
		username := strings.TrimSpace(loginMeta.Username)
		rememberMe := loginMeta.RememberMe

		wa, err := webauthnForRequest(c)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, err.Error()})
		}
		sess, _ := session.Get("session", c)

		if username == "" {
			token, _ := sess.Values[passkeyDiscoverableSessionKey].(string)
			if token == "" {
				return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Login session not found"})
			}
			passkeyLoginMu.Lock()
			sessionData, ok := passkeyLoginSessions[token]
			passkeyLoginMu.Unlock()
			if !ok {
				return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Login session expired"})
			}
			wUser, cred, err := wa.FinishPasskeyLogin(discoverableUserLookup(db), sessionData, c.Request())
			if err != nil {
				log.Infof("Discoverable passkey login failed: %v", err)
				return c.JSON(http.StatusUnauthorized, jsonHTTPResponse{false, "Invalid passkey"})
			}
			wu, ok := wUser.(webAuthnUser)
			if !ok {
				return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, "Invalid user state"})
			}
			u := wu.u
			if u.Disabled {
				return c.JSON(http.StatusUnauthorized, jsonHTTPResponse{false, "Cuenta deshabilitada"})
			}
			if err := setLoginSession(c, u, rememberMe, globalSettings.SessionTimeoutMinutes); err != nil {
				return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, fmt.Sprintf("Cannot set session: %v", err)})
			}
			freshSess, _ := session.Get("session", c)
			if cred != nil && len(cred.ID) > 0 {
				freshSess.Values[sessionPkLoginCredKey] = base64.RawURLEncoding.EncodeToString(cred.ID)
			}
			delete(freshSess.Values, passkeyDiscoverableSessionKey)
			passkeyLoginMu.Lock()
			delete(passkeyLoginSessions, token)
			passkeyLoginMu.Unlock()
			_ = freshSess.Save(c.Request(), c.Response())
			return c.JSON(http.StatusOK, jsonHTTPResponse{true, "Logged in successfully"})
		}

		if !usernameRegexp.MatchString(username) {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Please provide a valid username"})
		}
		u, err := db.GetUserByName(username)
		if err != nil {
			return c.JSON(http.StatusUnauthorized, jsonHTTPResponse{false, "Invalid credentials"})
		}
		if u.Disabled {
			return c.JSON(http.StatusUnauthorized, jsonHTTPResponse{false, "Cuenta deshabilitada"})
		}
		token, _ := sess.Values["passkey_login_"+username].(string)
		if token == "" {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Login session not found"})
		}
		passkeyLoginMu.Lock()
		sessionData, ok := passkeyLoginSessions[token]
		passkeyLoginMu.Unlock()
		if !ok {
			return c.JSON(http.StatusBadRequest, jsonHTTPResponse{false, "Login session expired"})
		}
		cred, err := wa.FinishLogin(webAuthnUser{u}, sessionData, c.Request())
		if err != nil {
			log.Infof("Passkey login failed for %s: %v", username, err)
			return c.JSON(http.StatusUnauthorized, jsonHTTPResponse{false, "Invalid passkey"})
		}
		if err := setLoginSession(c, u, rememberMe, globalSettings.SessionTimeoutMinutes); err != nil {
			return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, fmt.Sprintf("Cannot set session: %v", err)})
		}
		freshSess, _ := session.Get("session", c)
		if cred != nil && len(cred.ID) > 0 {
			freshSess.Values[sessionPkLoginCredKey] = base64.RawURLEncoding.EncodeToString(cred.ID)
		}
		delete(freshSess.Values, "passkey_login_"+username)
		passkeyLoginMu.Lock()
		delete(passkeyLoginSessions, token)
		passkeyLoginMu.Unlock()
		_ = freshSess.Save(c.Request(), c.Response())
		return c.JSON(http.StatusOK, jsonHTTPResponse{true, "Logged in successfully"})
	}
}
