package controlplane_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"go-software-factory/internal/controlplane"
	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/core/dsl"
	"go-software-factory/internal/engine"
	"go-software-factory/internal/engine/app/processdefinition"
	engineconfig "go-software-factory/internal/engine/config"
	"go-software-factory/internal/engine/infra/storage"
)

func TestControlPlaneProcessExecutions(t *testing.T) {
	const (
		csrfField    = "_csrf"
		messageField = "input.message"
		sourceField  = "input.source"
		sourcesField = "input.sources"
		payloadField = "input.payload"
		validMessage = "hello"
	)
	ctx := t.Context()
	t.Setenv("FACTORY_SQLITE_PATH", filepath.Join(t.TempDir(), "execution-ui.db"))
	t.Setenv("FACTORY_STORAGE_MODE", "sqlite")
	t.Setenv("FACTORY_PROCESS_EXECUTOR_RATE", "1h")
	t.Setenv("ADMIN_USERNAME", "admin")
	t.Setenv("ADMIN_PASSWORD", "execution-test-password")
	t.Setenv("WEB_AUTH_SECRET_KEY", "01234567890123456789012345678901")
	t.Setenv("PRODUCTION", "false")
	eng, err := engine.NewEngine()
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Stop() })
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FACTORY_WEB_SERVER_PORT", strconv.Itoa(port))
	plane, err := controlplane.NewControlPlane(eng)
	if err != nil {
		t.Fatal(err)
	}
	if err := plane.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plane.Stop() })
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path string, form url.Values, status int) (string, http.Header) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method,
			fmt.Sprintf("http://127.0.0.1:%d", port)+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, response.StatusCode, status, data[:min(len(data), 512)])
		}

		return string(data), response.Header
	}
	tokenFrom := func(body string) string {
		t.Helper()
		match := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatal("missing CSRF token")
		}

		return match[1]
	}
	request(http.MethodGet, "/processes/", nil, http.StatusFound)
	login, _ := request(http.MethodGet, "/auth/login", nil, http.StatusOK)
	request(http.MethodPost, "/processes/choose-directory", url.Values{csrfField: {tokenFrom(login)}}, http.StatusFound)
	request(http.MethodPost, "/auth/login", url.Values{
		"username": {"admin"}, "password": {"execution-test-password"}, csrfField: {tokenFrom(login)},
	}, http.StatusSeeOther)
	request(http.MethodPost, "/processes/choose-directory", nil, http.StatusBadRequest)
	empty, _ := request(http.MethodGet, "/processes/", nil, http.StatusOK)
	if !strings.Contains(empty, "No executions yet") {
		t.Fatal("empty execution history missing")
	}
	definition, err := dsl.ProcessDefinitionDslParse([]byte(`name: 'Inspect <script>alert(1)</script>'
inputs:
  message: {type: STRING, required: true}
  source: {type: REFERENCE}
  sources: {type: REFERENCES_ARRAY}
  payload: {type: JSON}
reliability: {max_retries: 10}
steps:
  inspect:
    type: AI_CODING_AGENT
    inputs:
      working_directory: {type: FILE_PATH, value: /tmp}
      prompt: {type: STRING, value: Inspect this execution}
      output_json: {type: JSON_SCHEMA, value: {type: object}}
    next: [end]
  end: {type: TERMINAL}
`))
	if err != nil {
		t.Fatal(err)
	}
	definition, err = eng.CreateProcessDefinitionUseCase.Create(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	definitionPath := "/process-definitions/" + definition.ID.String()
	startPath := definitionPath + "/execute"
	filteredPath := "/processes/?process_definition_id=" + definition.ID.String()
	detail, _ := request(http.MethodGet, definitionPath, nil, http.StatusOK)
	if !strings.Contains(detail, "flowchart TB") || !strings.Contains(detail, "data-process-graph") ||
		!strings.Contains(detail, "endEvent") {
		t.Fatal("definition does not render the shared top-to-bottom graph with an end event")
	}
	if !strings.Contains(detail, `href="`+startPath+`"`) {
		t.Fatal("definition does not link to execution form")
	}
	if !strings.Contains(detail, `href="`+filteredPath+`"`) || !strings.Contains(detail, "View processes") {
		t.Fatal("definition does not link to its filtered executions")
	}
	formPage, _ := request(http.MethodGet, startPath, nil, http.StatusOK)
	token := tokenFrom(formPage)
	for _, field := range []string{messageField, sourceField, sourcesField, payloadField} {
		if !strings.Contains(formPage, `name="`+field+`"`) {
			t.Fatalf("declared input %q missing", field)
		}
	}
	request(http.MethodPost, startPath, url.Values{messageField: {validMessage}}, http.StatusBadRequest)
	for _, invalid := range []url.Values{
		{}, {messageField: {"  "}},
		{messageField: {validMessage}, "input.unknown": {"injected"}},
		{messageField: {"one", "two"}},
		{messageField: {validMessage}, payloadField: {"invalid json"}},
	} {
		invalid.Set(csrfField, token)
		body, _ := request(http.MethodPost, startPath, invalid, http.StatusUnprocessableEntity)
		if !strings.Contains(body, "Start execution") {
			t.Fatal("invalid form did not retain the execution form")
		}
		if invalid.Get(payloadField) == "invalid json" &&
			(!strings.Contains(body, "invalid json") || !strings.Contains(body, validMessage)) {
			t.Fatal("validation error discarded the submitted inputs")
		}
	}
	for _, malformed := range []string{"%ZZ", "%", "a;b"} {
		raw := "_csrf=" + url.QueryEscape(token) + "&input.message=hello&input.payload=" + malformed
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			fmt.Sprintf("http://127.0.0.1:%d", port)+startPath, strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("malformed form %q returned %d; wanted 400 without creating an execution",
				malformed, response.StatusCode)
		}
	}
	history, err := eng.GetProcessUseCase.GetAll(ctx)
	if err != nil || len(history) != 0 {
		t.Fatalf("invalid requests created executions: %+v, %v", history, err)
	}
	_, headers := request(http.MethodPost, startPath, url.Values{
		csrfField: {token}, messageField: {"hello <script>alert(1)</script>"},
		sourceField: {"PROCESS:source"}, sourcesField: {`["PROCESS:first","PROCESS:second"]`},
		payloadField: {`{"message":"hello"}`},
	}, http.StatusSeeOther)
	executionPath := headers.Get("Location")
	id, err := uuid.Parse(strings.TrimPrefix(executionPath, "/processes/"))
	if err != nil {
		t.Fatalf("invalid execution redirect: %q", executionPath)
	}
	p, err := eng.GetProcessUseCase.GetByID(ctx, id)
	if err != nil || p.ProcessDefinitionID != definition.ID || p.Context == nil ||
		p.Context.Inputs.String("message") != "hello <script>alert(1)</script>" ||
		p.Context.Inputs.Get("source").Type != domain.ParameterTypeReference ||
		p.Context.Inputs.Get("sources").Type != domain.ParameterTypeReferencesArray ||
		p.Context.Inputs.Get("payload").Type != domain.ParameterTypeJSON {
		t.Fatalf("execution inputs were not persisted correctly: %+v, %v", p, err)
	}
	detail, _ = request(http.MethodGet, executionPath, nil, http.StatusOK)
	if !strings.Contains(detail, `aria-current="step"`) || strings.Contains(detail, "<script>alert(1)</script>") ||
		!strings.Contains(detail, "hello &lt;script&gt;") || !strings.Contains(detail, definitionPath) {
		t.Fatal("execution detail lost inputs, definition link, empty steps, or escaping")
	}
	if !strings.Contains(detail, "data-process-graph") || !strings.Contains(detail, "flowchart TB") ||
		!strings.Contains(detail, `id="step0-detail"`) || !strings.Contains(detail, "endEvent") {
		t.Fatal("execution graph omits unqueued steps, end event, or clickable step details")
	}
	now := time.Now().UTC()
	result, err := domain.NewParameters(map[string]domain.ParameterValue{
		"error":  {Type: domain.ParameterTypeString, Value: "worker <script>alert(1)</script> failed"},
		"output": {Type: domain.ParameterTypeJSON, Value: `{"summary":"Inspected <script>alert(1)</script> safely"}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	step := &domain.ProcessStep{
		ID: uuid.New(), ProcessID: id, ProcessStepDefinitionName: "inspect", Type: domain.ProcessStepTypeAICodingAgent,
		State: domain.ProcessStepStateFailed, Version: 1, CreatedAt: now, UpdatedAt: now,
		Reliability: domain.ProcessReliability{Executions: 2, ExecutionTime: 3 * time.Second},
		Params:      p.Context.Inputs,
		Result:      result,
	}
	if err := eng.ProcessStepRepository.Save(ctx, step); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.FailProcessUseCase.Fail(ctx, p, fmt.Errorf("worker failed")); err != nil {
		t.Fatal(err)
	}
	detail, _ = request(http.MethodGet, executionPath, nil, http.StatusOK)
	for _, expected := range []string{
		"Failed", "AI_CODING_AGENT", "3s", "worker &lt;script&gt;", "Inspected &lt;script&gt;",
	} {
		if !strings.Contains(detail, expected) {
			t.Fatalf("step detail missing %q", expected)
		}
	}
	if strings.Contains(detail, "<script>alert(1)</script>") {
		t.Fatal("step payload was rendered as HTML")
	}
	if !strings.Contains(detail, `id="process-step-dialog"`) ||
		!strings.Contains(detail, `id="step1-detail"`) || !strings.Contains(detail, "Not reached") {
		t.Fatal("execution does not expose step details or shows an unreached end event as executed")
	}
	catalog, _ := request(http.MethodGet, "/processes/", nil, http.StatusOK)
	if !strings.Contains(catalog, executionPath) || !strings.Contains(catalog, ">Failed<") {
		t.Fatal("failed execution missing from history")
	}
	for _, path := range []string{"/processes/invalid", "/processes/" + uuid.New().String(),
		"/process-definitions/invalid/execute", "/process-definitions/" + uuid.New().String() + "/execute"} {
		request(http.MethodGet, path, nil, http.StatusNotFound)
	}
	cfg, err := engineconfig.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", cfg.Storage.SqliteConfig.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_ui_execution BEFORE INSERT ON processes
BEGIN SELECT RAISE(ABORT, 'private insert failure'); END;`); err != nil {
		t.Fatal(err)
	}
	failed, _ := request(http.MethodPost, startPath, url.Values{
		csrfField: {token}, messageField: {"recover this input"},
	}, http.StatusInternalServerError)
	if !strings.Contains(failed, "recover this input") || strings.Contains(failed, "private insert failure") {
		t.Fatal("creation error discarded input or exposed storage internals")
	}
	if _, err := db.ExecContext(ctx, "DROP TRIGGER reject_ui_execution"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		"UPDATE process_definitions SET state='ARCHIVED' WHERE id=?", definition.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	request(http.MethodGet, startPath, nil, http.StatusConflict)
	request(http.MethodPost, startPath, url.Values{csrfField: {token}, messageField: {validMessage}}, http.StatusConflict)
	history, err = eng.GetProcessUseCase.GetAll(ctx)
	if err != nil || len(history) != 1 {
		t.Fatal("inactive definition created an execution")
	}
	noInputs, err := dsl.ProcessDefinitionDslParse([]byte("name: No-input example\nsteps: {end: {type: TERMINAL}}"))
	if err != nil {
		t.Fatal(err)
	}
	noInputs, err = eng.CreateProcessDefinitionUseCase.Create(ctx, noInputs)
	if err != nil {
		t.Fatal(err)
	}
	noInputPath := "/process-definitions/" + noInputs.ID.String() + "/execute"
	noInputForm, _ := request(http.MethodGet, noInputPath, nil, http.StatusOK)
	_, started := request(http.MethodPost, noInputPath, url.Values{
		csrfField: {tokenFrom(noInputForm)},
	}, http.StatusSeeOther)
	request(http.MethodGet, started.Get("Location"), nil, http.StatusOK)
	additional, err := eng.CreateProcessUseCase.Create(ctx, noInputs.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	otherPaths := []string{started.Get("Location"), "/processes/" + additional.ID.String()}
	filtered, _ := request(http.MethodGet, filteredPath, nil, http.StatusOK)
	if !strings.Contains(filtered, executionPath) {
		t.Fatal("definition filter omitted its execution")
	}
	for _, path := range otherPaths {
		if strings.Contains(filtered, path) {
			t.Fatal("definition filter included another definition's execution")
		}
	}
	if !strings.Contains(filtered, `value="`+definition.ID.String()+`" selected`) ||
		!strings.Contains(filtered, `name="process_definition_id"`) ||
		!strings.Contains(filtered, `href="`+filteredPath+`"`) || !strings.Contains(filtered, "Clear filter") {
		t.Fatal("selected filter, refresh link, or clear action missing")
	}
	if strings.Contains(filtered, "<script>alert(1)</script>") {
		t.Fatal("filter option rendered the definition name as HTML")
	}
	otherFiltered, _ := request(http.MethodGet,
		"/processes/?process_definition_id="+noInputs.ID.String(), nil, http.StatusOK)
	for _, path := range otherPaths {
		if !strings.Contains(otherFiltered, path) {
			t.Fatal("filter omitted one of the matching executions")
		}
	}
	if strings.Contains(otherFiltered, executionPath) {
		t.Fatal("filter included an execution belonging to the previous selection")
	}
	for _, path := range []string{"/processes/", "/processes/?process_definition_id="} {
		all, _ := request(http.MethodGet, path, nil, http.StatusOK)
		for _, execution := range append(otherPaths, executionPath) {
			if !strings.Contains(all, execution) {
				t.Fatal("clearing the filter did not restore all executions")
			}
		}
	}
	unknownID := uuid.New().String()
	noMatches, _ := request(http.MethodGet, "/processes/?process_definition_id="+unknownID, nil, http.StatusOK)
	if !strings.Contains(noMatches, "No executions for this definition") ||
		!strings.Contains(noMatches, `value="`+unknownID+`" selected`) || strings.Contains(noMatches, executionPath) {
		t.Fatal("empty filtered history did not retain the selected filter")
	}
	request(http.MethodGet, "/processes/?process_definition_id=invalid", nil, http.StatusBadRequest)
	archivedView, _ := request(http.MethodGet, definitionPath, nil, http.StatusOK)
	if !strings.Contains(archivedView, `href="`+filteredPath+`"`) {
		t.Fatal("archived definition lost its execution history link")
	}
	if err := eng.Stop(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/processes/", executionPath, startPath} {
		body, _ := request(http.MethodGet, path, nil, http.StatusInternalServerError)
		if strings.Contains(body, "database is closed") {
			t.Fatal("storage internals leaked to the browser")
		}
	}
}

func TestControlPlaneCatalog(t *testing.T) {
	const csrfField = "_csrf"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("ADMIN_USERNAME", "admin")
	t.Setenv("ADMIN_PASSWORD", "test-admin-password")
	t.Setenv("WEB_AUTH_SECRET_KEY", "01234567890123456789012345678901")
	t.Setenv("PRODUCTION", "false")
	t.Setenv("FACTORY_SQLITE_PATH", path)
	t.Setenv("FACTORY_STORAGE_MODE", "sqlite")
	eng, err := engine.NewEngine()
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := eng.Stop(); err != nil {
			t.Error(err)
		}
	})
	listenConfig := &net.ListenConfig{}
	listener, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FACTORY_WEB_SERVER_PORT", strconv.Itoa(port))
	plane, err := controlplane.NewControlPlane(eng)
	if err != nil {
		t.Fatal(err)
	}
	if err := plane.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := plane.Stop(); err != nil {
			t.Error(err)
		}
	})
	cfg, err := engineconfig.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	stg, err := storage.NewStorage(cfg.Storage)
	if err != nil {
		t.Fatal(err)
	}
	if err := stg.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stg.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	create := processdefinition.NewCreateProcessDefinitionUseCase(stg.ProcessDefinitionRepository)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	get := func(path string, headers map[string]string, status int) string {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d", port)+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := response.Body.Close(); err != nil {
				t.Error(err)
			}
		}()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status {
			t.Fatalf("GET %s: status %d; want %d: %s", path, response.StatusCode, status, body)
		}

		return string(body)
	}
	get("/process-definitions/", nil, http.StatusFound)
	get("/", nil, http.StatusFound)
	loginPage := get("/auth/login", nil, http.StatusOK)
	token := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(loginPage)
	if len(token) != 2 {
		t.Fatal("CSRF token missing from login form")
	}
	loginForm := url.Values{"username": {"admin"}, "password": {"test-admin-password"}, csrfField: {token[1]}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/auth/login", port), strings.NewReader(loginForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/process-definitions/" {
		t.Fatalf("login: %d", response.StatusCode)
	}
	body := get("/process-definitions/", nil, http.StatusOK)
	if !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, "No process definitions yet") {
		t.Fatalf("empty catalog page missing: %s", body)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for i, fixture := range []struct {
		name  string
		state domain.ProcessDefinitionState
	}{
		{"Release <script>alert(1)</script>", domain.ProcessDefinitionStateActive},
		{"Documentation sync", domain.ProcessDefinitionStateDraft},
		{"Old release", domain.ProcessDefinitionStateArchived},
	} {
		definition, err := domain.NewProcessDefinition(
			domain.WithNewProcessDefinitionID(), domain.WithProcessDefinitionName(fixture.name),
			domain.WithProcessDefinitionDescription("Engineering workflow"),
			domain.WithProcessDefinitionState(fixture.state), domain.WithProcessDefinitionVersion(1),
			domain.WithProcessDefinitionCreatedAt(now),
			domain.WithProcessDefinitionUpdatedAt(now.Add(time.Duration(i)*time.Minute)),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := create.Create(ctx, definition); err != nil {
			t.Fatal(err)
		}
	}
	body = get("/process-definitions/", nil, http.StatusOK)
	if !strings.Contains(body, "<!doctype html>") ||
		!strings.Contains(body, "Release &lt;script&gt;") ||
		strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("list rendering or escaping failed: %s", body)
	}
	for _, name := range []string{"Documentation sync", "Old release"} {
		if !strings.Contains(body, name) {
			t.Fatalf("definition %q missing from list", name)
		}
	}
	if !strings.Contains(body, "<style>") || !strings.Contains(body, ".app-header") {
		t.Fatal("control-plane stylesheet missing")
	}
	creationPage := get("/process-definitions/new", nil, http.StatusOK)
	creationToken := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(creationPage)
	if len(creationToken) != 2 {
		t.Fatal("missing creation CSRF token")
	}
	creationForm := url.Values{
		csrfField: {creationToken[1]},
		"yaml":    {"name: Created through HTTP\nsteps: {build: {next: [test]}, test: {}}"},
	}
	creationRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/process-definitions/", port), strings.NewReader(creationForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	creationRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	creationResponse, err := client.Do(creationRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := creationResponse.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if creationResponse.StatusCode != http.StatusSeeOther ||
		!strings.HasPrefix(creationResponse.Header.Get("Location"), "/process-definitions/") {
		t.Fatalf("creation: %d", creationResponse.StatusCode)
	}
	detailPath := creationResponse.Header.Get("Location")
	detail := get(detailPath, nil, http.StatusOK)
	if !strings.Contains(detail, "Workflow") || !strings.Contains(detail, "Created through HTTP") {
		t.Fatal("creation redirect did not display the definition")
	}
	edit := get(detailPath+"?mode=edit", nil, http.StatusOK)
	editToken := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(edit)
	if len(editToken) != 2 {
		t.Fatal("missing edit CSRF token")
	}
	editVersion := regexp.MustCompile(`name="version" value="([^"]+)"`).FindStringSubmatch(edit)
	if len(editVersion) != 2 || editVersion[1] != "1" {
		t.Fatal("missing edit version")
	}
	editForm := url.Values{
		"version": {editVersion[1]},
		csrfField: {editToken[1]},
		"yaml":    {"name: Updated through HTTP\nsteps: {build: {next: [review]}, review: {next: [build]}}"},
	}
	editRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d", port)+detailPath, strings.NewReader(editForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	editRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	editResponse, err := client.Do(editRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := editResponse.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if editResponse.StatusCode != http.StatusSeeOther || editResponse.Header.Get("Location") != detailPath {
		t.Fatalf("update: %d", editResponse.StatusCode)
	}
	if body := get(detailPath, nil, http.StatusOK); !strings.Contains(body, "Updated through HTTP") {
		t.Fatal("updated detail missing")
	}
	if body := get("/process-definitions/", nil, http.StatusOK); !strings.Contains(body, "Updated through HTTP") {
		t.Fatal("HTTP-created definition missing")
	}
	if err := eng.Stop(); err != nil {
		t.Fatal(err)
	}
	body = get("/process-definitions/", nil, http.StatusInternalServerError)
	if !strings.Contains(body, `"error":"internal server error"`) {
		t.Fatal("database failure was hidden")
	}
}
