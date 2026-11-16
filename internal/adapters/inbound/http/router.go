package httpadapter

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/application/port"
	"github.com/Orang2-BU/pantara-back/internal/application/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

// RouterDeps bundles all inbound HTTP adapter dependencies cleanly.
type RouterDeps struct {
	AuthUC             *usecase.AuthUseCase
	WorkspaceUC        *usecase.WorkspaceUseCase
	CatalogUC          *usecase.CatalogUseCase
	TaskUC             *usecase.TaskUseCase
	SelfUC             *usecase.SelfUseCase
	ReportNotifUC      *usecase.ReportAndNotifUseCase
	Security           port.SecurityStore
	HealthCheck        func(ctx context.Context) error
	CORSAllowedOrigins []string
	CookieSecure       bool
	CookieDomain       string
	IncludeDevTokens   bool
	Logger             *slog.Logger
}

// NewRouter builds the complete Chi HTTP router for Pantara v1 endpoints, RBAC guards, and health checks.
func NewRouter(deps RouterDeps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(requestLogger(deps.Logger))
	r.Use(middleware.Compress(5))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   deps.CORSAllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-Id"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Liveness & readiness probes (required for Dokploy / load balancers)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, map[string]string{"status": "ok", "service": "pantara-api"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if deps.HealthCheck != nil {
			if err := deps.HealthCheck(r.Context()); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, ErrorEnvelope{
					Error: ErrorBody{Code: "NOT_READY", Message: "Database belum siap menerima permintaan."},
				})
				return
			}
		}
		writeOK(w, map[string]string{"status": "ready"})
	})

	cookies := cookieConfig{Secure: deps.CookieSecure, Domain: deps.CookieDomain}
	ah := &authHandlers{uc: deps.AuthUC, cookies: cookies}
	wh := &workspaceHandlers{uc: deps.WorkspaceUC}
	ch := &catalogHandlers{uc: deps.CatalogUC, includeDevTokens: deps.IncludeDevTokens}
	th := &taskHandlers{uc: deps.TaskUC}
	sh := &selfHandlers{uc: deps.SelfUC}
	nh := &analyticsHandlers{uc: deps.ReportNotifUC}

	r.Route("/api/v1", func(api chi.Router) {
		// Public auth endpoints (tight brute-force rate limit on login/register/forgot-password)
		api.Group(func(pub chi.Router) {
			pub.With(authRateLimit(deps.Security, 30, time.Minute)).Post("/auth/register", ah.register)
			pub.With(authRateLimit(deps.Security, 30, time.Minute)).Post("/auth/login", ah.login)
			pub.With(authRateLimit(deps.Security, 20, time.Minute)).Post("/auth/forgot-password", ah.forgotPassword)
			pub.Post("/auth/refresh", ah.refresh)
			pub.Post("/auth/logout", ah.logout)
			pub.Post("/auth/reset-password", ah.resetPassword)
			pub.Get("/auth/invitations/{token}", ah.inspectInvite)
			pub.Post("/auth/accept-invite", ah.acceptInvite)
		})

		// Authenticated tenant-scoped routes
		api.Group(func(authed chi.Router) {
			authed.Use(authenticate(deps.AuthUC))
			authed.Use(apiRateLimit(deps.Security))

			// Account & session self-management (all signed-in roles)
			authed.Get("/auth/me", ah.me)
			authed.Post("/auth/logout-all", ah.logoutAll)
			authed.Patch("/account/profile", ah.updateProfile)
			authed.Post("/account/password", ah.changePassword)
			authed.Patch("/account/notifications", ah.updateNotifyPrefs)

			// Shared bootstrap & periods (read by all authenticated roles; org-scoped in usecase)
			authed.Get("/workspace/bootstrap", wh.bootstrap)
			authed.Get("/workspace/periods", wh.listPeriods)
			authed.Get("/competencies", ch.listCompetencies)
			authed.Get("/projects", ch.listProjects)
			authed.Get("/projects/{id}", ch.getProject)
			authed.Get("/tasks/{id}", th.get) // role=anggota restricted to own assigned tasks inside GetTask
			authed.Get("/notifications", nh.listNotifications)
			authed.Post("/notifications/read-all", nh.markAllNotificationsRead)
			authed.Post("/notifications/{id}/read", nh.markNotificationRead)
			authed.Get("/notifications/stream", nh.streamNotifications)

			// Employee Self-Service (/saya)
			authed.Get("/saya/overview", sh.overview)
			authed.Patch("/saya/profile", sh.updateProfile)
			authed.Put("/saya/availability", sh.updateAvailability)
			authed.Post("/saya/tasks/{id}/respond", sh.respondTask)
			authed.Patch("/saya/tasks/{id}/progress", sh.updateProgress)

			// Manager + Admin routes
			authed.Group(func(mgr chi.Router) {
				mgr.Use(requireManager)

				// Competencies CRUD
				mgr.Post("/competencies", ch.createCompetency)
				mgr.Patch("/competencies/{id}", ch.updateCompetency)
				mgr.Delete("/competencies/{id}", ch.deleteCompetency)

				// Projects CRUD
				mgr.Post("/projects", ch.createProject)
				mgr.Patch("/projects/{id}", ch.updateProject)
				mgr.Delete("/projects/{id}", ch.deleteProject)

				// Team & Member operations
				mgr.Get("/members", ch.listMembers)
				mgr.Get("/members/{id}", ch.getMember)
				mgr.Post("/members", ch.createMember)
				mgr.Post("/members/{id}/invite-resend", ch.resendInvite)
				mgr.Patch("/members/{id}", ch.updateMember)
				mgr.Put("/members/{id}/availability", ch.updateMemberAvailability)
				mgr.Post("/members/{id}/competencies/{compId}/verify", ch.verifyCompetency)
				mgr.Post("/members/{id}/remind", ch.remindMember)

				// Tasks, AI Task DNA, Recommendation, Simulation, Assignment & Batch
				mgr.Get("/tasks", th.list)
				mgr.Post("/tasks", th.create)
				mgr.Patch("/tasks/{id}", th.update)
				mgr.Delete("/tasks/{id}", th.deleteOrArchive)
				mgr.Post("/tasks/{id}/flow-start", th.flowStart)
				mgr.Post("/tasks/{id}/analyze-dna", th.analyzeDNA)
				mgr.Put("/tasks/{id}/confirm-dna", th.confirmDNA)
				mgr.Get("/tasks/{id}/candidates", th.candidates)
				mgr.Post("/tasks/{id}/simulate", th.simulate)
				mgr.Post("/tasks/{id}/assign", th.assign)
				mgr.Post("/tasks/{id}/reject", th.reject)
				mgr.Post("/tasks/{id}/unassign", th.unassign)
				mgr.Patch("/tasks/{id}/status", th.updateStatus)
				mgr.Get("/batch/preview", th.batchPreview)
				mgr.Post("/batch/commit", th.batchCommit)

				// Analytics & Decision Log
				mgr.Get("/decisions", nh.listDecisions)
				mgr.Get("/report", nh.report)
				mgr.Patch("/report/baseline", nh.updateBaseline)
				mgr.Get("/capacity", nh.capacity)
				mgr.Get("/skill-map", nh.skillMap)
				mgr.Get("/relation-graph", nh.relationGraph)
			})

			// Admin-only routes (organization settings, onboarding, work periods, engine weights, member access/active, billing)
			authed.Group(func(adm chi.Router) {
				adm.Use(requireAdmin)

				adm.Get("/workspace", wh.getOrg)
				adm.Patch("/workspace", wh.updateOrg)
				adm.Post("/workspace/onboarding", wh.completeOnboarding)
				adm.Post("/workspace/periods", wh.createPeriod)
				adm.Post("/workspace/periods/{id}/activate", wh.activatePeriod)
				adm.Get("/workspace/settings", wh.getSettings)
				adm.Patch("/workspace/settings", wh.updateSettings)
				adm.Get("/workspace/billing", wh.getBilling)
				adm.Patch("/members/{id}/access", ch.setMemberAccessAndActive)
			})
		})
	})

	return r
}
