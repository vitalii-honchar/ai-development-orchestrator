package web

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/gorilla/securecookie"
	"go-software-factory/internal/controlplane/infra/web/handler"
	authhandler "go-software-factory/internal/controlplane/infra/web/handler/auth"
	"go-software-factory/internal/controlplane/infra/web/handler/processdefinition"
	"go-software-factory/internal/controlplane/infra/web/middleware"
	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/core/dsl"
	"go-software-factory/internal/engine"
	appauth "go-software-factory/internal/engine/app/auth"
	infraauth "go-software-factory/internal/engine/infra/auth"
)

const (
	testSecret        = "01234567890123456789012345678901"
	testLoginPath     = "/auth/login"
	testCSRFField     = "_csrf"
	testSessionName   = "factory-session"
	testAdminUsername = "admin"
	testVersionField  = "version"
	testYAMLField     = "yaml"
	testPasswordField = "password"
)

func TestAuthenticationFlow(t *testing.T) {
	t.Setenv("FACTORY_SQLITE_PATH", filepath.Join(t.TempDir(), "auth.db"))
	t.Setenv("ADMIN_USERNAME", testAdminUsername)
	t.Setenv("ADMIN_PASSWORD", "test password")
	eng, err := engine.NewEngine()
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := eng.Stop(); err != nil {
			t.Error(err)
		}
	}()
	private := []handler.Handler{processdefinition.NewListView(eng.GetProcessDefinitionUseCase)}
	web := NewWebServer(&WebConfig{AuthSecretKey: testSecret}, nil, private, eng.LoginUseCase)
	request := func(method, path string, form url.Values, cookie *http.Cookie, htmx bool) *httptest.ResponseRecorder {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		response := httptest.NewRecorder()
		web.server.Handler.ServeHTTP(response, req)

		return response
	}
	for _, path := range []string{"/", "/process-definitions/"} {
		response := request("GET", path, nil, nil, false)
		if response.Code != 302 || response.Header().Get("Location") != testLoginPath {
			t.Fatalf("anonymous %s = %d", path, response.Code)
		}
	}
	response := request("GET", "/process-definitions/", nil, nil, true)
	if response.Code != 401 || response.Header().Get("HX-Redirect") != testLoginPath {
		t.Fatal("HTMX auth redirect missing")
	}
	page := request("GET", testLoginPath, nil, nil, false)
	if page.Code != 200 ||
		!strings.Contains(page.Body.String(), "Sign in") ||
		strings.Contains(page.Body.String(), "Main navigation") {
		t.Fatal("invalid login page")
	}
	token := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(token) != 2 {
		t.Fatal("missing CSRF token")
	}
	csrfCookie := page.Result().Cookies()[0]
	form := url.Values{"username": {testAdminUsername}, testPasswordField: {"test password"}, testCSRFField: {token[1]}}
	if response := request("POST", testLoginPath, form, nil, false); response.Code != 400 {
		t.Fatal("CSRF requires session")
	}
	form.Set(testCSRFField, "bad")
	if response := request("POST", testLoginPath, form, csrfCookie, false); response.Code != 400 {
		t.Fatal("invalid CSRF accepted")
	}
	form.Del(testCSRFField)
	if response := request("POST", testLoginPath, form, csrfCookie, false); response.Code != 400 {
		t.Fatal("missing CSRF accepted")
	}
	form.Set(testCSRFField, token[1])
	for _, identity := range []string{testAdminUsername, "unknown", "Admin"} {
		form.Set("username", identity)
		form.Set(testPasswordField, "wrong")
		for _, htmx := range []bool{false, true} {
			response := request("POST", testLoginPath, form, csrfCookie, htmx)
			if response.Code != 401 ||
				!strings.Contains(response.Body.String(), "Invalid username or password") ||
				response.Header().Get("HX-Redirect") != "" {
				t.Fatalf("invalid credentials: %d %s", response.Code, response.Body.String())
			}
			if htmx && strings.Contains(response.Body.String(), "<!doctype") {
				t.Fatal("HTMX error should be a fragment")
			}
		}
	}
	form.Set("username", testAdminUsername)
	form.Set(testPasswordField, "test password")
	response = request("POST", testLoginPath, form, csrfCookie, true)
	if response.Code != 200 || response.Header().Get("HX-Redirect") != authhandler.CatalogPath {
		t.Fatalf("login = %d %s", response.Code, response.Body.String())
	}
	session := response.Result().Cookies()[0]
	if session.Name != testSessionName ||
		!session.HttpOnly ||
		session.Secure ||
		session.SameSite != http.SameSiteLaxMode ||
		session.Path != "/" ||
		session.MaxAge != 30*24*60*60 {
		t.Fatal("incorrect cookie options")
	}
	for _, path := range []string{"/", testLoginPath} {
		response := request("GET", path, nil, session, false)
		if response.Code != 302 || response.Header().Get("Location") != authhandler.CatalogPath {
			t.Fatal("authenticated redirect missing")
		}
	}
	if response := request("GET", authhandler.CatalogPath, nil, session, false); response.Code != 200 ||
		!strings.Contains(response.Body.String(), "No process definitions yet") {
		t.Fatalf("catalog = %d %s", response.Code, response.Body.String())
	}
	response = request("POST", testLoginPath, form, csrfCookie, false)
	if response.Code != 303 || response.Header().Get("Location") != authhandler.CatalogPath {
		t.Fatal("ordinary login redirect missing")
	}
	// The same signing key permits sessions to survive construction of a new server.
	web = NewWebServer(&WebConfig{AuthSecretKey: testSecret, Production: true}, nil, private, eng.LoginUseCase)
	if response := request("GET", authhandler.CatalogPath, nil, session, false); response.Code != 200 {
		t.Fatal("session lost across server restart")
	}
	response = request("POST", testLoginPath, form, csrfCookie, true)
	if response.Code != 200 || !response.Result().Cookies()[0].Secure {
		t.Fatal("production cookie must be Secure")
	}
	codec := securecookie.New([]byte(testSecret), nil)
	for _, value := range []any{nil, "", 123} {
		encoded, err := codec.Encode(testSessionName, map[interface{}]interface{}{middleware.SessionParamUsername: value})
		if err != nil {
			t.Fatal(err)
		}
		cookie := &http.Cookie{
			Name: session.Name, Value: encoded, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		}
		if response := request("GET", authhandler.CatalogPath, nil, cookie, false); response.Code != 302 {
			t.Fatal("invalid identity accepted")
		}
	}
	for _, value := range []string{
		"invalid", session.Value[:len(session.Value)/2], expiredCookie(t, session.Value),
		session.Value[:len(session.Value)-4] + "AAAA",
	} {
		cookie := &http.Cookie{
			Name: session.Name, Value: value, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		}
		response := request("GET", authhandler.CatalogPath, nil, cookie, false)
		if response.Code != 302 {
			t.Fatalf("invalid or expired session accepted: %d", response.Code)
		}
	}
	web = NewWebServer(&WebConfig{AuthSecretKey: strings.Repeat("x", 32)}, nil, private, eng.LoginUseCase)
	if response := request("GET", authhandler.CatalogPath, nil, session, false); response.Code != 302 {
		t.Fatal("old key accepted")
	}
	web = NewWebServer(&WebConfig{AuthSecretKey: testSecret}, nil, private, eng.LoginUseCase)
	if err := eng.Stop(); err != nil {
		t.Fatal(err)
	}
	if response := request("POST", testLoginPath, form, csrfCookie, true); response.Code != 500 ||
		response.Header().Get("HX-Redirect") != "" {
		t.Fatal("repository error hidden")
	}
}

// Re-sign a real cookie with an old timestamp so this exercises server-side expiry,
// not just signature rejection or the browser's Max-Age behavior.
func expiredCookie(t *testing.T, value string) string {
	t.Helper()
	raw, err := base64.URLEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	parts := bytes.SplitN(raw, []byte("|"), 3)
	payload := fmt.Sprintf("%d|%s", time.Now().Add(-31*24*time.Hour).Unix(), parts[1])
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte("factory-session|" + payload))

	return base64.URLEncoding.EncodeToString(append([]byte(payload+"|"), mac.Sum(nil)...))
}

type oversizedUserRepository struct{ user *domain.User }

func (r oversizedUserRepository) GetByUsername(context.Context, string) (*domain.User, error) {
	return r.user, nil
}

func (r oversizedUserRepository) Create(context.Context, *domain.User) error {
	return nil
}

func TestSessionSaveFailure(t *testing.T) {
	t.Parallel()
	hash, err := infraauth.Hash("password")
	if err != nil {
		t.Fatal(err)
	}
	// A valid identity too large for a cookie forces the actual store's Save to fail.
	useCase := appauth.NewLoginUseCase(oversizedUserRepository{&domain.User{
		Username: strings.Repeat("u", 5000), Password: hash, State: domain.UserStateActive,
	}}, infraauth.Verify)
	server := NewWebServer(&WebConfig{AuthSecretKey: testSecret}, nil, nil, useCase)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	page := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(page, httptest.NewRequestWithContext(ctx, "GET", testLoginPath, nil))
	token := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(token) != 2 {
		t.Fatal("missing CSRF token")
	}
	form := url.Values{"username": {testAdminUsername}, testPasswordField: {"password"}, testCSRFField: {token[1]}}
	req := httptest.NewRequestWithContext(ctx, "POST", testLoginPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(page.Result().Cookies()[0])
	response := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(response, req)
	if response.Code != 500 || response.Header().Get("HX-Redirect") != "" || len(response.Result().Cookies()) != 0 {
		t.Fatalf("session save failure = %d", response.Code)
	}
}

func TestCreateProcessDefinition(t *testing.T) {
	t.Setenv("FACTORY_SQLITE_PATH", filepath.Join(t.TempDir(), "definitions.db"))
	t.Setenv("ADMIN_USERNAME", testAdminUsername)
	t.Setenv("ADMIN_PASSWORD", "test password")
	eng, err := engine.NewEngine()
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := eng.Stop(); err != nil {
			t.Error(err)
		}
	}()
	server := NewWebServer(&WebConfig{AuthSecretKey: testSecret}, nil, []handler.Handler{
		processdefinition.NewListView(eng.GetProcessDefinitionUseCase),
		processdefinition.NewCreateView(),
		processdefinition.NewCreate(eng.CreateProcessDefinitionUseCase),
		processdefinition.NewDetailView(eng.GetProcessDefinitionUseCase),
		processdefinition.NewUpdate(eng.GetProcessDefinitionUseCase, eng.UpdateProcessDefinitionUseCase),
	}, eng.LoginUseCase)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	request := func(method, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		server.server.Handler.ServeHTTP(response, req)

		return response
	}
	if response := request("GET", "/process-definitions/new", nil, nil); response.Code != http.StatusFound ||
		response.Header().Get("Location") != testLoginPath {
		t.Fatal("anonymous creation page allowed")
	}
	// A valid CSRF token must not grant authentication to the POST handler.
	login := request("GET", testLoginPath, nil, nil)
	tokenPattern := regexp.MustCompile(`name="_csrf" value="([^"]+)"`)
	anonymousToken := tokenPattern.FindStringSubmatch(login.Body.String())
	if len(anonymousToken) != 2 {
		t.Fatal("missing anonymous CSRF token")
	}
	if response := request("POST", "/process-definitions/", url.Values{
		testCSRFField: {anonymousToken[1]},
		testYAMLField: {"{}"},
	}, login.Result().Cookies()[0]); response.Code != http.StatusFound {
		t.Fatal("anonymous creation allowed")
	}
	encoded, err := securecookie.New([]byte(testSecret), nil).Encode(testSessionName, map[interface{}]interface{}{
		middleware.SessionParamUsername: testAdminUsername,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := &http.Cookie{
		Name: testSessionName, Value: encoded, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	}
	page := request("GET", "/process-definitions/new", nil, session)
	if page.Code != http.StatusOK {
		t.Fatalf("editor: %d %s", page.Code, page.Body.String())
	}
	token := tokenPattern.FindStringSubmatch(page.Body.String())
	if len(token) != 2 {
		t.Fatal("missing creation token")
	}
	session = page.Result().Cookies()[0]
	example := regexp.MustCompile(`(?s)<textarea[^>]*>(.*?)</textarea>`).FindStringSubmatch(page.Body.String())
	if !strings.Contains(page.Body.String(), "saved as Active, version 1") {
		t.Fatal("creation page does not describe active state")
	}
	if len(example) != 2 {
		t.Fatal("missing example textarea")
	}
	for _, source := range []string{"", "bad"} {
		if response := request("POST", "/process-definitions/", url.Values{
			testCSRFField: {source},
			testYAMLField: {"{}"},
		}, session); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid CSRF: %d", response.Code)
		}
	}
	invalid := "name: '</textarea><script>alert(1)</script>'\nsteps: {build: {next: [missing]}}"
	response := request("POST", "/process-definitions/", url.Values{
		testCSRFField: {token[1]},
		testYAMLField: {invalid},
	}, session)
	if response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), "unknown step") ||
		strings.Contains(response.Body.String(), "<script>alert(1)</script>") {
		t.Fatalf("invalid submission: %d %s", response.Code, response.Body.String())
	}
	retained := regexp.MustCompile(`(?s)<textarea[^>]*>(.*?)</textarea>`).FindStringSubmatch(response.Body.String())
	if len(retained) != 2 || html.UnescapeString(retained[1]) != invalid {
		t.Fatal("invalid YAML not preserved")
	}
	definitions, err := eng.GetProcessDefinitionUseCase.GetAll(ctx)
	if err != nil || len(definitions) != 0 {
		t.Fatalf("invalid requests saved data: %v %v", definitions, err)
	}
	response = request("POST", "/process-definitions/", url.Values{
		testCSRFField: {token[1]},
		testYAMLField: {html.UnescapeString(example[1])},
	}, session)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	definitions, err = eng.GetProcessDefinitionUseCase.GetAll(ctx)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("created definitions: %v %v", definitions, err)
	}
	definition := definitions[0]
	if response.Header().Get("Location") != "/process-definitions/"+definition.ID.String() {
		t.Fatal("creation did not redirect to the new definition")
	}
	if definition.State != domain.ProcessDefinitionStateActive ||
		definition.Version != 1 ||
		len(definition.Graph.Nodes) != 4 ||
		len(definition.Graph.IncomingEdges["publish"]) != 2 {
		t.Fatalf("incorrect persisted graph: %+v", definition)
	}

	duplicateSource := html.UnescapeString(example[1])
	duplicate := request("POST", "/process-definitions/", url.Values{
		testCSRFField: {token[1]},
		testYAMLField: {duplicateSource},
	}, session)
	duplicateText := regexp.MustCompile(`(?s)<textarea[^>]*>(.*?)</textarea>`).FindStringSubmatch(duplicate.Body.String())
	if duplicate.Code != http.StatusConflict ||
		!strings.Contains(duplicate.Body.String(), "already exists") ||
		len(duplicateText) != 2 ||
		html.UnescapeString(duplicateText[1]) != duplicateSource {
		t.Fatalf("duplicate creation not explained or preserved: %d %s", duplicate.Code, duplicate.Body.String())
	}
	definitions, err = eng.GetProcessDefinitionUseCase.GetAll(ctx)
	if err != nil || len(definitions) != 1 {
		t.Fatal("duplicate creation inserted a record")
	}
	catalog := request("GET", "/process-definitions/", nil, session)
	if catalog.Code != http.StatusOK ||
		!strings.Contains(catalog.Body.String(), "Release") ||
		strings.Contains(catalog.Body.String(), "monaco-editor") {
		t.Fatal("created definition missing or editor assets leaked into catalog")
	}
	if err := eng.Stop(); err != nil {
		t.Fatal(err)
	}
	source := "name: Saved text\nsteps: {}"
	response = request("POST", "/process-definitions/", url.Values{
		testCSRFField: {token[1]},
		testYAMLField: {source},
	}, session)
	if response.Code != http.StatusInternalServerError ||
		!strings.Contains(response.Body.String(), "Unable to create process definition") ||
		!strings.Contains(response.Body.String(), source) ||
		strings.Contains(response.Body.String(), "database is closed") {
		t.Fatalf("persistence failure: %d %s", response.Code, response.Body.String())
	}
}

func TestProcessDefinitionDetailAndEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "detail.db")
	t.Setenv("FACTORY_SQLITE_PATH", path)
	t.Setenv("ADMIN_USERNAME", testAdminUsername)
	t.Setenv("ADMIN_PASSWORD", "test password")
	eng, err := engine.NewEngine()
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := eng.Stop(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	original, err := dsl.ProcessDefinitionDslParse([]byte(`name: 'Release <script>alert(1)</script>'
inputs: {source: {type: REFERENCE, required: true}}
reliability: {timeout: 10m}
steps:
  build: {type: custom.build, next: [review]}
  review: {next: [build]}
`))
	if err != nil {
		t.Fatal(err)
	}
	original.State = domain.ProcessDefinitionStateActive
	original.Version = 3
	original, err = eng.CreateProcessDefinitionUseCase.Create(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	detailPath := "/process-definitions/" + original.ID.String()
	server := NewWebServer(&WebConfig{AuthSecretKey: testSecret}, nil, []handler.Handler{
		processdefinition.NewListView(eng.GetProcessDefinitionUseCase),
		processdefinition.NewDetailView(eng.GetProcessDefinitionUseCase),
		processdefinition.NewUpdate(eng.GetProcessDefinitionUseCase, eng.UpdateProcessDefinitionUseCase),
	}, eng.LoginUseCase)
	request := func(method, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		server.server.Handler.ServeHTTP(response, req)

		return response
	}
	for _, route := range []string{detailPath, detailPath + "?mode=edit"} {
		if response := request("GET", route, nil, nil); response.Code != http.StatusFound ||
			response.Header().Get("Location") != testLoginPath {
			t.Fatal("anonymous detail access allowed")
		}
	}
	tokenPattern := regexp.MustCompile(`name="_csrf" value="([^"]+)"`)
	login := request("GET", testLoginPath, nil, nil)
	anonymousToken := tokenPattern.FindStringSubmatch(login.Body.String())
	if len(anonymousToken) != 2 {
		t.Fatal("missing anonymous token")
	}
	if response := request("POST", detailPath, url.Values{
		testCSRFField: {anonymousToken[1]},
		testYAMLField: {"{}"},
	}, login.Result().Cookies()[0]); response.Code != http.StatusFound {
		t.Fatal("anonymous update allowed")
	}
	encoded, err := securecookie.New([]byte(testSecret), nil).Encode(testSessionName, map[interface{}]interface{}{
		middleware.SessionParamUsername: testAdminUsername,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := &http.Cookie{
		Name: testSessionName, Value: encoded, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	}
	for _, route := range []string{"/process-definitions/not-a-uuid", "/process-definitions/" + uuid.New().String()} {
		for _, mode := range []string{"", "?mode=edit"} {
			if response := request("GET", route+mode, nil, session); response.Code != http.StatusNotFound {
				t.Fatalf("missing ID status: %d", response.Code)
			}
		}
	}
	page := request("GET", detailPath, nil, session)
	if page.Code != http.StatusOK ||
		!strings.Contains(page.Body.String(), "Workflow") ||
		!strings.Contains(page.Body.String(), "step0 --&gt; step1") ||
		!strings.Contains(page.Body.String(), "step1 --&gt; step0") ||
		strings.Contains(page.Body.String(), "<script>alert(1)</script>") ||
		strings.Contains(page.Body.String(), "monaco-editor") ||
		strings.Contains(page.Body.String(), "<textarea") {
		t.Fatalf("invalid read-only detail: %d %s", page.Code, page.Body.String())
	}
	catalog := request("GET", "/process-definitions/", nil, session)
	if !strings.Contains(catalog.Body.String(), `href="`+detailPath+`"`) {
		t.Fatal("catalog does not link to detail")
	}
	edit := request("GET", detailPath+"?mode=edit", nil, session)
	token := tokenPattern.FindStringSubmatch(edit.Body.String())
	if edit.Code != http.StatusOK ||
		len(token) != 2 ||
		!strings.Contains(edit.Body.String(), "Save changes") ||
		!strings.Contains(edit.Body.String(), "monaco-editor") ||
		strings.Contains(edit.Body.String(), "mermaid@") {
		t.Fatalf("invalid edit page: %d %s", edit.Code, edit.Body.String())
	}
	session = edit.Result().Cookies()[0]
	textareaPattern := regexp.MustCompile(`(?s)<textarea[^>]*>(.*?)</textarea>`)
	source := textareaPattern.FindStringSubmatch(edit.Body.String())
	if len(source) != 2 {
		t.Fatal("missing edit YAML")
	}
	restored, err := dsl.ProcessDefinitionDslParse([]byte(html.UnescapeString(source[1])))
	if err != nil ||
		!reflect.DeepEqual(restored.Graph, original.Graph) ||
		restored.Reliability != original.Reliability ||
		!reflect.DeepEqual(restored.Inputs, original.Inputs) {
		t.Fatalf("edit YAML lost content: %+v %v", restored, err)
	}
	for _, csrfToken := range []string{"", "invalid"} {
		if response := request("POST", detailPath, url.Values{
			testYAMLField: {"{}"},
			testCSRFField: {csrfToken},
		}, session); response.Code != http.StatusBadRequest {
			t.Fatal("update accepted invalid CSRF")
		}
	}
	if response := request("POST", "/process-definitions/"+uuid.New().String(), url.Values{
		testYAMLField: {"{}"},
		testCSRFField: {token[1]},
	}, session); response.Code != http.StatusNotFound {
		t.Fatal("update inserted missing definition")
	}
	invalid := "name: '</textarea><script>alert(1)</script>'\nsteps: {build: {next: [missing]}}"
	response := request("POST", detailPath, url.Values{
		testVersionField: {strconv.Itoa(original.Version)},
		testYAMLField:    {invalid},
		testCSRFField:    {token[1]},
	}, session)
	retained := textareaPattern.FindStringSubmatch(response.Body.String())
	if response.Code != http.StatusUnprocessableEntity ||
		len(retained) != 2 ||
		html.UnescapeString(retained[1]) != invalid ||
		strings.Contains(response.Body.String(), "<script>alert(1)</script>") {
		t.Fatalf("invalid edit was not preserved: %d %s", response.Code, response.Body.String())
	}
	unchanged, err := eng.GetProcessDefinitionUseCase.GetByID(ctx, original.ID)
	if err != nil || !reflect.DeepEqual(unchanged, original) {
		t.Fatal("invalid edits changed storage")
	}
	valid := "name: Updated release\nsteps: {build: {next: [publish]}, publish: {}}"
	response = request("POST", detailPath, url.Values{
		testVersionField: {strconv.Itoa(original.Version)},
		testYAMLField:    {valid},
		testCSRFField:    {token[1]},
	}, session)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != detailPath {
		t.Fatalf("update redirect: %d", response.Code)
	}
	updated, err := eng.GetProcessDefinitionUseCase.GetByID(ctx, original.ID)
	if err != nil ||
		updated.Name != "Updated release" ||
		updated.State != original.State ||
		updated.Version != original.Version+1 ||
		!updated.CreatedAt.Equal(original.CreatedAt) ||
		!updated.UpdatedAt.After(original.UpdatedAt) ||
		updated.Graph.Nodes["publish"] == nil {
		t.Fatalf("incorrect update: %+v %v", updated, err)
	}
	definitions, err := eng.GetProcessDefinitionUseCase.GetAll(ctx)
	if err != nil || len(definitions) != 1 {
		t.Fatal("update created another record")
	}

	stale := request("POST", detailPath, url.Values{
		testVersionField: {strconv.Itoa(original.Version)},
		testYAMLField:    {valid},
		testCSRFField:    {token[1]},
	}, session)
	if stale.Code != http.StatusConflict ||
		!strings.Contains(stale.Body.String(), "changed since you opened") ||
		!strings.Contains(stale.Body.String(), `name="version" value="1"`) ||
		!strings.Contains(stale.Body.String(), valid) {
		t.Fatalf("stale edit not rejected safely: %d %s", stale.Code, stale.Body.String())
	}
	for _, version := range []string{"", "bad", "0", "-1"} {
		response := request("POST", detailPath, url.Values{
			testVersionField: {version},
			testYAMLField:    {valid},
			testCSRFField:    {token[1]},
		}, session)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid version accepted: %q %d", version, response.Code)
		}
	}
	current, err := eng.GetProcessDefinitionUseCase.GetByID(ctx, original.ID)
	if err != nil || !reflect.DeepEqual(current, updated) {
		t.Fatal("stale or invalid edit changed storage")
	}

	other, err := dsl.ProcessDefinitionDslParse([]byte("name: Taken name"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.CreateProcessDefinitionUseCase.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	duplicateEdit := "name: Taken name\nsteps: {}"
	response = request("POST", detailPath, url.Values{
		testVersionField: {strconv.Itoa(updated.Version)},
		testYAMLField:    {duplicateEdit},
		testCSRFField:    {token[1]},
	}, session)
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "already exists") ||
		!strings.Contains(response.Body.String(), duplicateEdit) {
		t.Fatalf("duplicate rename not explained: %d %s", response.Code, response.Body.String())
	}
	current, err = eng.GetProcessDefinitionUseCase.GetByID(ctx, original.ID)
	if err != nil || !reflect.DeepEqual(current, updated) {
		t.Fatal("duplicate rename changed definition")
	}
	// Fail the write while reads still work, so the edit page can retain input.
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_detail_update BEFORE UPDATE ON process_definitions
BEGIN SELECT RAISE(ABORT, 'private storage error'); END;`); err != nil {
		t.Fatal(err)
	}
	response = request("POST", detailPath, url.Values{
		testVersionField: {strconv.Itoa(updated.Version)},
		testYAMLField:    {valid},
		testCSRFField:    {token[1]},
	}, session)
	if response.Code != http.StatusInternalServerError ||
		!strings.Contains(response.Body.String(), "Unable to save process definition") ||
		!strings.Contains(response.Body.String(), valid) ||
		strings.Contains(response.Body.String(), "private storage error") {
		t.Fatal("storage failure did not preserve edit safely")
	}
	if err := eng.Stop(); err != nil {
		t.Fatal(err)
	}
	if response := request("GET", detailPath, nil, session); response.Code != http.StatusInternalServerError ||
		!strings.Contains(response.Body.String(), "Unable to load process definition") {
		t.Fatal("load failure hidden")
	}
}
