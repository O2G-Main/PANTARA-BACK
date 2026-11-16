package httpadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/adapters/outbound/security"
	"github.com/Orang2-BU/pantara-back/internal/adapters/outbound/sqlstore"
	"github.com/Orang2-BU/pantara-back/internal/app"
	"github.com/Orang2-BU/pantara-back/internal/config"
	"github.com/Orang2-BU/pantara-back/internal/seeddata"
)

type testEnv struct {
	t         *testing.T
	container *app.Container
	server    *httptest.Server
}

func newTestEnv(t *testing.T, seed bool) *testEnv {
	t.Helper()
	store, err := sqlstore.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite memory: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		_ = store.Close()
		t.Fatalf("migrate sqlite: %v", err)
	}
	cfg := config.Config{
		AppEnv:             "test",
		CORSAllowedOrigins: []string{"http://localhost:3000"},
		JWTSecret:          "pantara-unit-test-secret-32-bytes-minimum-key",
		AppBaseURL:         "http://localhost:3000",
		AIModel:            "gpt-4o-mini",
		AITimeout:          5 * time.Second,
	}
	c := app.NewContainer(cfg, store, security.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if seed {
		if _, err := c.SeedIfEmpty(context.Background(), false); err != nil {
			_ = c.Close()
			t.Fatalf("seed arunika: %v", err)
		}
	}
	srv := httptest.NewServer(c.Handler)
	t.Cleanup(func() {
		srv.Close()
		_ = c.Close()
	})
	return &testEnv{t: t, container: c, server: srv}
}

func (e *testEnv) do(method, path, token string, cookies []*http.Cookie, body any) (*http.Response, []byte) {
	e.t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.server.URL+path, rdr)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s failed: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func (e *testEnv) login(email, password string) (string, []*http.Cookie, *http.Response, []byte) {
	resp, b := e.do("POST", "/api/v1/auth/login", "", nil, map[string]any{
		"email":      email,
		"password":   password,
		"rememberMe": true,
	})
	if resp.StatusCode != http.StatusOK {
		return "", resp.Cookies(), resp, b
	}
	var parsed struct {
		AccessToken string `json:"accessToken"`
	}
	_ = json.Unmarshal(b, &parsed)
	return parsed.AccessToken, resp.Cookies(), resp, b
}

func TestHealthAndAuthLifecycle(t *testing.T) {
	env := newTestEnv(t, true)

	// 1. Health & readiness
	rH, _ := env.do("GET", "/healthz", "", nil, nil)
	if rH.StatusCode != http.StatusOK {
		t.Fatalf("healthz got %d", rH.StatusCode)
	}
	rR, _ := env.do("GET", "/readyz", "", nil, nil)
	if rR.StatusCode != http.StatusOK {
		t.Fatalf("readyz got %d", rR.StatusCode)
	}

	// 2. Unauthenticated bootstrap must be 401
	rUnauth, _ := env.do("GET", "/api/v1/workspace/bootstrap", "", nil, nil)
	if rUnauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated bootstrap, got %d", rUnauth.StatusCode)
	}

	// 3. Login + HttpOnly Cookie auth
	tok, cookies, resp, _ := env.login("krisna@pantara.id", seeddata.DemoPassword)
	if resp.StatusCode != http.StatusOK || tok == "" || len(cookies) < 2 {
		t.Fatalf("expected login 200 with token + 2 HttpOnly cookies, got %d, cookies=%d", resp.StatusCode, len(cookies))
	}
	for _, c := range cookies {
		if !c.HttpOnly {
			t.Fatalf("cookie %s must be HttpOnly", c.Name)
		}
	}

	// Cookie auth works on /auth/me without Bearer header
	rMe, _ := env.do("GET", "/api/v1/auth/me", "", cookies, nil)
	if rMe.StatusCode != http.StatusOK {
		t.Fatalf("cookie /auth/me failed: %d", rMe.StatusCode)
	}

	// 4. Refresh rotation revokes previous refresh token
	rRef1, bRef1 := env.do("POST", "/api/v1/auth/refresh", "", cookies, nil)
	if rRef1.StatusCode != http.StatusOK {
		t.Fatalf("refresh 1 failed: %d %s", rRef1.StatusCode, string(bRef1))
	}
	// Reusing old refresh cookie must fail 401!
	rRefOld, _ := env.do("POST", "/api/v1/auth/refresh", "", cookies, nil)
	if rRefOld.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected reused refresh cookie 401, got %d", rRefOld.StatusCode)
	}

	// 5. Logout-all immediately revokes prior access JWTs via session_version bump
	rOutAll, _ := env.do("POST", "/api/v1/auth/logout-all", tok, nil, nil)
	if rOutAll.StatusCode != http.StatusOK {
		t.Fatalf("logout-all failed: %d", rOutAll.StatusCode)
	}
	rStale, _ := env.do("GET", "/api/v1/auth/me", tok, nil, nil)
	if rStale.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected old JWT to be invalidated immediately after logout-all, got %d", rStale.StatusCode)
	}

	// 6. Lockout after 5 failed attempts -> 1..4 are 401, 5th locks account with 423 Locked
	for i := 0; i < 4; i++ {
		_, _, rFail, _ := env.login("manajer@pantara.id", "wrong-password-123")
		if rFail.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d expected 401, got %d", i+1, rFail.StatusCode)
		}
	}
	_, _, rFifth, _ := env.login("manajer@pantara.id", "wrong-password-123")
	if rFifth.StatusCode != http.StatusLocked {
		t.Fatalf("expected 423 Locked on 5th failed attempt, got %d", rFifth.StatusCode)
	}
	_, _, rLocked, _ := env.login("manajer@pantara.id", seeddata.DemoPassword)
	if rLocked.StatusCode != http.StatusLocked {
		t.Fatalf("expected 423 Locked even with valid password while locked, got %d", rLocked.StatusCode)
	}
}

func TestRBACAndCrossTenantIsolation(t *testing.T) {
	env := newTestEnv(t, true)

	adminTok, _, _, _ := env.login("krisna@pantara.id", seeddata.DemoPassword)
	mgrTok, _, _, _ := env.login("manajer@pantara.id", seeddata.DemoPassword)
	bimaTok, _, _, _ := env.login("bima@pantara.id", seeddata.DemoPassword)

	// 1. Register completely isolated Tenant B org
	rReg, bReg := env.do("POST", "/api/v1/auth/register", "", nil, map[string]any{
		"orgName":  "Studio Nusantara",
		"name":     "Citra Dewi",
		"email":    "citra@nusantara.id",
		"password": "password1234",
	})
	if rReg.StatusCode != http.StatusCreated {
		t.Fatalf("register tenant B failed: %d %s", rReg.StatusCode, string(bReg))
	}
	var regParsed struct {
		AccessToken string `json:"accessToken"`
	}
	_ = json.Unmarshal(bReg, &regParsed)
	tenantBTok := regParsed.AccessToken

	// Tenant B cannot see Tenant A's TGS-0142 or projects
	rCrossTask, _ := env.do("GET", "/api/v1/tasks/TGS-0142", tenantBTok, nil, nil)
	if rCrossTask.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 cross-tenant task access, got %d", rCrossTask.StatusCode)
	}

	// 2. Anggota RBAC restrictions
	for _, ep := range []struct{ m, p string }{
		{"GET", "/api/v1/members"},
		{"GET", "/api/v1/report"},
		{"GET", "/api/v1/tasks/TGS-0142"}, // unassigned to Bima
		{"POST", "/api/v1/tasks"},
		{"POST", "/api/v1/projects"},
	} {
		rForb, _ := env.do(ep.m, ep.p, bimaTok, nil, map[string]any{"name": "x"})
		if rForb.StatusCode != http.StatusForbidden {
			t.Fatalf("anggota %s %s expected 403, got %d", ep.m, ep.p, rForb.StatusCode)
		}
	}

	// 3. Manager cannot hit Admin-only settings, billing, or member access change
	for _, ep := range []struct{ m, p string }{
		{"PATCH", "/api/v1/workspace"},
		{"GET", "/api/v1/workspace/settings"},
		{"PATCH", "/api/v1/workspace/settings"},
		{"GET", "/api/v1/workspace/billing"},
		{"PATCH", "/api/v1/members/bima/access"},
	} {
		rForb, _ := env.do(ep.m, ep.p, mgrTok, nil, map[string]any{})
		if rForb.StatusCode != http.StatusForbidden {
			t.Fatalf("manajer %s %s expected 403, got %d", ep.m, ep.p, rForb.StatusCode)
		}
	}

	// 4. Admin succeeds on /workspace/billing
	rBill, _ := env.do("GET", "/api/v1/workspace/billing", adminTok, nil, nil)
	if rBill.StatusCode != http.StatusOK {
		t.Fatalf("admin billing expected 200, got %d", rBill.StatusCode)
	}
}

func TestFullWorkflowCRUDAndSelfService(t *testing.T) {
	env := newTestEnv(t, true)
	adminTok, _, _, _ := env.login("krisna@pantara.id", seeddata.DemoPassword)
	bimaTok, _, _, _ := env.login("bima@pantara.id", seeddata.DemoPassword)

	// 1. Competencies CRUD
	rC1, bC1 := env.do("POST", "/api/v1/competencies", adminTok, nil, map[string]any{
		"name":     "GraphQL Federation",
		"category": "Backend",
	})
	if rC1.StatusCode != http.StatusCreated {
		t.Fatalf("create competency failed: %d %s", rC1.StatusCode, string(bC1))
	}
	var compCreated struct {
		Competency struct {
			ID string `json:"id"`
		} `json:"competency"`
	}
	_ = json.Unmarshal(bC1, &compCreated)
	rC2, _ := env.do("PATCH", "/api/v1/competencies/"+compCreated.Competency.ID, adminTok, nil, map[string]any{
		"name":     "GraphQL Mesh",
		"category": "Backend",
	})
	if rC2.StatusCode != http.StatusOK {
		t.Fatalf("update competency failed: %d", rC2.StatusCode)
	}
	rC3, _ := env.do("DELETE", "/api/v1/competencies/"+compCreated.Competency.ID, adminTok, nil, nil)
	if rC3.StatusCode != http.StatusOK {
		t.Fatalf("delete competency failed: %d", rC3.StatusCode)
	}

	// 2. Projects CRUD
	dueNextWeek := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	rP1, bP1 := env.do("POST", "/api/v1/projects", adminTok, nil, map[string]any{
		"name":    "Portal Finansial",
		"client":  "PT Maju Bersama",
		"dueDate": dueNextWeek,
	})
	if rP1.StatusCode != http.StatusCreated {
		t.Fatalf("create project failed: %d %s", rP1.StatusCode, string(bP1))
	}
	var projCreated struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	_ = json.Unmarshal(bP1, &projCreated)

	// 3. Create Task + AI Analyze DNA + Confirm DNA + Candidates + Simulate + Assign to Bima
	rT1, bT1 := env.do("POST", "/api/v1/tasks", adminTok, nil, map[string]any{
		"projectId":   projCreated.Project.ID,
		"title":       "Bangun Gateway OAuth2 Backend API",
		"description": "Implementasi endpoint backend API berbasis OAuth2 dengan audit log PostgreSQL.",
		"dueDate":     dueNextWeek,
		"priority":    "tinggi",
		"draft":       false,
	})
	if rT1.StatusCode != http.StatusCreated {
		t.Fatalf("create task failed: %d %s", rT1.StatusCode, string(bT1))
	}
	var taskCreated struct {
		Task struct {
			ID string `json:"id"`
		} `json:"task"`
	}
	_ = json.Unmarshal(bT1, &taskCreated)
	taskID := taskCreated.Task.ID

	rDNA, bDNA := env.do("POST", "/api/v1/tasks/"+taskID+"/analyze-dna", adminTok, nil, nil)
	if rDNA.StatusCode != http.StatusOK {
		t.Fatalf("analyze DNA failed: %d %s", rDNA.StatusCode, string(bDNA))
	}
	var dnaResp struct {
		Task struct {
			DNA struct {
				Requirements []any   `json:"requirements"`
				Complexity   int     `json:"complexity"`
				EffortHours  float64 `json:"effortHours"`
				EffortSize   string  `json:"effortSize"`
			} `json:"dna"`
		} `json:"task"`
	}
	_ = json.Unmarshal(bDNA, &dnaResp)

	rConf, bConf := env.do("PUT", "/api/v1/tasks/"+taskID+"/confirm-dna", adminTok, nil, map[string]any{
		"requirements": dnaResp.Task.DNA.Requirements,
		"complexity":   3,
		"effortHours":  4,
		"effortSize":   "S",
		"dependencies": []any{},
	})
	if rConf.StatusCode != http.StatusOK {
		t.Fatalf("confirm DNA failed: %d %s", rConf.StatusCode, string(bConf))
	}

	rCand, _ := env.do("GET", "/api/v1/tasks/"+taskID+"/candidates?preset=seimbang", adminTok, nil, nil)
	if rCand.StatusCode != http.StatusOK {
		t.Fatalf("candidates failed: %d", rCand.StatusCode)
	}

	rSim, _ := env.do("POST", "/api/v1/tasks/"+taskID+"/simulate", adminTok, nil, map[string]any{
		"assignments": []map[string]any{{"memberId": "bima", "hours": 4}},
	})
	if rSim.StatusCode != http.StatusOK {
		t.Fatalf("simulate failed: %d", rSim.StatusCode)
	}

	rAsg, bAsg := env.do("POST", "/api/v1/tasks/"+taskID+"/assign", adminTok, nil, map[string]any{
		"assignments": []map[string]any{{"memberId": "bima", "hours": 4}},
		"preset":      "seimbang",
	})
	if rAsg.StatusCode != http.StatusOK {
		t.Fatalf("assign failed: %d %s", rAsg.StatusCode, string(bAsg))
	}

	// 4. Employee /saya sees task, accepts, updates progress, completes!
	rOv, bOv := env.do("GET", "/api/v1/saya/overview", bimaTok, nil, nil)
	if rOv.StatusCode != http.StatusOK || !strings.Contains(string(bOv), taskID) {
		t.Fatalf("bima overview missing new task %s (status=%d)", taskID, rOv.StatusCode)
	}

	rAcc, _ := env.do("POST", "/api/v1/saya/tasks/"+taskID+"/respond", bimaTok, nil, map[string]any{"accept": true})
	if rAcc.StatusCode != http.StatusOK {
		t.Fatalf("bima accept failed: %d", rAcc.StatusCode)
	}

	rDone, bDone := env.do("PATCH", "/api/v1/saya/tasks/"+taskID+"/progress", bimaTok, nil, map[string]any{
		"complete":    true,
		"actualHours": 3.5,
	})
	if rDone.StatusCode != http.StatusOK || !strings.Contains(string(bDone), `"selesai"`) {
		t.Fatalf("bima complete failed: %d %s", rDone.StatusCode, string(bDone))
	}

	// 5. Invite workflow + accept + activate
	rInv, bInv := env.do("POST", "/api/v1/members", adminTok, nil, map[string]any{
		"name":   "Zaki Ramadhan",
		"email":  "zaki@pantara.id",
		"role":   "QA Engineer",
		"access": "anggota",
	})
	if rInv.StatusCode != http.StatusCreated {
		t.Fatalf("invite failed: %d %s", rInv.StatusCode, string(bInv))
	}
	var invResp struct {
		InviteToken string `json:"inviteToken"`
	}
	_ = json.Unmarshal(bInv, &invResp)
	rInsp, _ := env.do("GET", "/api/v1/auth/invitations/"+invResp.InviteToken, "", nil, nil)
	if rInsp.StatusCode != http.StatusOK {
		t.Fatalf("inspect invite failed: %d", rInsp.StatusCode)
	}
	rJoin, _ := env.do("POST", "/api/v1/auth/accept-invite", "", nil, map[string]any{
		"token":    invResp.InviteToken,
		"name":     "Zaki Ramadhan",
		"password": "password1234",
	})
	if rJoin.StatusCode != http.StatusOK {
		t.Fatalf("accept invite failed: %d", rJoin.StatusCode)
	}

	// 6. Decision log append-only & batch allocation checks
	if err := env.container.Store.Ping(context.Background()); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	rDecs, bDecs := env.do("GET", "/api/v1/decisions", adminTok, nil, nil)
	if rDecs.StatusCode != http.StatusOK || !strings.Contains(string(bDecs), taskID) {
		t.Fatalf("decisions log missing assigned task %s (status=%d)", taskID, rDecs.StatusCode)
	}
	rBatchPrev, bBatchPrev := env.do("GET", "/api/v1/batch/preview?preset=seimbang", adminTok, nil, nil)
	if rBatchPrev.StatusCode != http.StatusOK || !strings.Contains(string(bBatchPrev), "pairs") {
		t.Fatalf("batch preview failed: %d %s", rBatchPrev.StatusCode, string(bBatchPrev))
	}
}

func TestConcurrency1000Users(t *testing.T) {
	env := newTestEnv(t, true)
	adminTok, _, _, _ := env.login("krisna@pantara.id", seeddata.DemoPassword)
	mgrTok, _, _, _ := env.login("manajer@pantara.id", seeddata.DemoPassword)

	// Confirm TGS-0142 TaskDNA first so /candidates and /assign exercise the confirmed workflow cleanly
	rDNA, bDNA := env.do("POST", "/api/v1/tasks/TGS-0142/analyze-dna", adminTok, nil, nil)
	if rDNA.StatusCode != http.StatusOK {
		t.Fatalf("seed analyze DNA failed: %d %s", rDNA.StatusCode, string(bDNA))
	}
	var dnaParsed struct {
		Task struct {
			DNA json.RawMessage `json:"dna"`
		} `json:"task"`
	}
	_ = json.Unmarshal(bDNA, &dnaParsed)
	rConf, bConf := env.do("PUT", "/api/v1/tasks/TGS-0142/confirm-dna", adminTok, nil, dnaParsed.Task.DNA)
	if rConf.StatusCode != http.StatusOK {
		t.Fatalf("seed confirm DNA failed: %d %s", rConf.StatusCode, string(bConf))
	}

	// Phase A1: 1000 truly simultaneous in-flight requests directly through the router & backend layers
	const totalReads = 1000
	var inFlight200 atomic.Int32
	var inFlightBad atomic.Int32
	var mu sync.Mutex
	badSamples := make([]string, 0, 5)
	recordBad := func(msg string) {
		inFlightBad.Add(1)
		mu.Lock()
		if len(badSamples) < 5 {
			badSamples = append(badSamples, msg)
		}
		mu.Unlock()
	}
	var wgInFlight sync.WaitGroup
	paths := []string{
		"/api/v1/workspace/bootstrap",
		"/api/v1/tasks",
		"/api/v1/report",
		"/api/v1/tasks/TGS-0142/candidates?preset=seimbang",
	}

	startGate := make(chan struct{})
	for i := 0; i < totalReads; i++ {
		wgInFlight.Add(1)
		go func(idx int) {
			defer wgInFlight.Done()
			<-startGate
			req := httptest.NewRequest("GET", paths[idx%len(paths)], nil)
			req.Header.Set("Authorization", "Bearer "+adminTok)
			rec := httptest.NewRecorder()
			env.container.Handler.ServeHTTP(rec, req)
			if rec.Code == http.StatusOK {
				inFlight200.Add(1)
			} else {
				recordBad(rec.Result().Status + " " + rec.Body.String())
			}
		}(i)
	}
	close(startGate)
	wgInFlight.Wait()
	if inFlight200.Load() != totalReads || inFlightBad.Load() != 0 {
		t.Fatalf("1000 simultaneous in-flight check: ok=%d bad=%d; samples=%v", inFlight200.Load(), inFlightBad.Load(), badSamples)
	}

	// Phase A2: 1000 real HTTP/TCP round-trips via pooled keepalive transport (separate user quota from A1)
	var tcp200 atomic.Int32
	var tcpBad atomic.Int32
	var tcpMu sync.Mutex
	tcpSamples := make([]string, 0, 5)
	var wgTCP sync.WaitGroup
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			MaxConnsPerHost:     100,
		},
	}

	for i := 0; i < totalReads; i++ {
		wgTCP.Add(1)
		go func(idx int) {
			defer wgTCP.Done()
			req, _ := http.NewRequest("GET", env.server.URL+paths[idx%len(paths)], nil)
			req.Header.Set("Authorization", "Bearer "+mgrTok)
			resp, err := client.Do(req)
			if err != nil {
				tcpBad.Add(1)
				tcpMu.Lock()
				if len(tcpSamples) < 5 {
					tcpSamples = append(tcpSamples, err.Error())
				}
				tcpMu.Unlock()
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				tcp200.Add(1)
			} else {
				tcpBad.Add(1)
				tcpMu.Lock()
				if len(tcpSamples) < 5 {
					tcpSamples = append(tcpSamples, resp.Status)
				}
				tcpMu.Unlock()
			}
		}(i)
	}
	wgTCP.Wait()
	if tcp200.Load() != totalReads || tcpBad.Load() != 0 {
		t.Fatalf("1000 TCP round-trip stress check: ok=%d bad=%d samples=%v", tcp200.Load(), tcpBad.Load(), tcpSamples)
	}

	// Pick rank-1 recommended candidate dynamically for contested race test
	rCand, bCand := env.do("GET", "/api/v1/tasks/TGS-0142/candidates?preset=seimbang", adminTok, nil, nil)
	if rCand.StatusCode != http.StatusOK {
		t.Fatalf("candidates lookup failed: %d %s", rCand.StatusCode, string(bCand))
	}
	var candParsed struct {
		Candidates []struct {
			Member struct {
				ID string `json:"id"`
			} `json:"member"`
		} `json:"candidates"`
	}
	_ = json.Unmarshal(bCand, &candParsed)
	if len(candParsed.Candidates) == 0 {
		t.Fatalf("expected at least 1 ranked candidate for TGS-0142, body=%s", string(bCand))
	}
	topMemberID := candParsed.Candidates[0].Member.ID

	// Phase B: 50 simultaneous contested POST /assign on open task TGS-0142 -> strictly 1 wins (200), 49 get Conflict (409)
	const assignContenders = 50
	var win200 atomic.Int32
	var conflict409 atomic.Int32
	var errOther atomic.Int32
	var errMu sync.Mutex
	errSamples := make([]string, 0, 3)
	var assignWg sync.WaitGroup
	payload, _ := json.Marshal(map[string]any{
		"assignments": []map[string]any{{"memberId": topMemberID, "hours": 4}},
		"preset":      "seimbang",
	})

	for i := 0; i < assignContenders; i++ {
		assignWg.Add(1)
		go func() {
			defer assignWg.Done()
			req, _ := http.NewRequest("POST", env.server.URL+"/api/v1/tasks/TGS-0142/assign", bytes.NewReader(payload))
			req.Header.Set("Authorization", "Bearer "+adminTok)
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				errOther.Add(1)
				errMu.Lock()
				if len(errSamples) < 3 {
					errSamples = append(errSamples, err.Error())
				}
				errMu.Unlock()
				return
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusOK:
				win200.Add(1)
			case http.StatusConflict:
				conflict409.Add(1)
			default:
				errOther.Add(1)
				errMu.Lock()
				if len(errSamples) < 3 {
					errSamples = append(errSamples, resp.Status+" "+string(b))
				}
				errMu.Unlock()
			}
		}()
	}
	assignWg.Wait()

	if win200.Load() != 1 || conflict409.Load() != assignContenders-1 || errOther.Load() != 0 {
		t.Fatalf("contested assign race check failed: win200=%d conflict409=%d other=%d samples=%v", win200.Load(), conflict409.Load(), errOther.Load(), errSamples)
	}
}
