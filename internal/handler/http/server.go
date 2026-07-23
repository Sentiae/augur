package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	pkgmiddleware "github.com/sentiae/platform-kit/middleware"
	"github.com/sentiae/platform-kit/opshttp"
	"github.com/sentiae/platform-kit/posture"

	"github.com/sentiae/infrastructure-intelligence-service/internal/usecase"
)

// Server is the HTTP server with all routes
type Server struct {
	router              chi.Router
	posture             *posture.Set
	jwks                pkgmiddleware.TokenValidator
	serviceAPIKey       string
	orgResolver         OrgResolver
	workloadService     *usecase.WorkloadService
	decisionEngine      *usecase.DecisionEngine
	sloEngine           *usecase.SLOEngine
	costAnalyzer        *usecase.CostAnalyzer
	anomalyDetector     *usecase.AnomalyDetector
	alertService        *usecase.AlertService
	rightsizingEng      *usecase.RightsizingEngine
	spotManager         *usecase.SpotManager
	predictionEngine    *usecase.PredictionEngine
	capacitySim         *usecase.CapacitySimulator
	multiLayerDetector  *usecase.MultiLayerAnomalyDetector
	riRecommender       *usecase.RIRecommender
	crossClusterOpt     *usecase.CrossClusterOptimizer
}

func NewServer(
	postureSet *posture.Set,
	jwks pkgmiddleware.TokenValidator,
	serviceAPIKey string,
	orgResolver OrgResolver,
	workloadService *usecase.WorkloadService,
	decisionEngine *usecase.DecisionEngine,
	sloEngine *usecase.SLOEngine,
	costAnalyzer *usecase.CostAnalyzer,
	anomalyDetector *usecase.AnomalyDetector,
	alertService *usecase.AlertService,
	rightsizingEng *usecase.RightsizingEngine,
	spotManager *usecase.SpotManager,
	predictionEngine *usecase.PredictionEngine,
	capacitySim *usecase.CapacitySimulator,
	multiLayerDetector *usecase.MultiLayerAnomalyDetector,
	riRecommender *usecase.RIRecommender,
	crossClusterOpt *usecase.CrossClusterOptimizer,
) *Server {
	s := &Server{
		posture:            postureSet,
		jwks:               jwks,
		serviceAPIKey:      serviceAPIKey,
		orgResolver:        orgResolver,
		workloadService:    workloadService,
		decisionEngine:     decisionEngine,
		sloEngine:          sloEngine,
		costAnalyzer:       costAnalyzer,
		anomalyDetector:    anomalyDetector,
		alertService:       alertService,
		rightsizingEng:     rightsizingEng,
		spotManager:        spotManager,
		predictionEngine:   predictionEngine,
		capacitySim:        capacitySim,
		multiLayerDetector: multiLayerDetector,
		riRecommender:      riRecommender,
		crossClusterOpt:    crossClusterOpt,
	}
	s.setupRoutes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) setupRoutes() {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		respondWithJSON(w, http.StatusOK, map[string]string{"status": "healthy", "service": "augur"})
	})

	r.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
		respondWithJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	// Wave-8 (D-179): enumerate the boot-proved fail-closed security controls.
	// Open (like /health, /ready) — introspection only, no secrets.
	if s.posture != nil {
		r.Method(http.MethodGet, "/posture", opshttp.PostureHandler("augur", s.posture))
	}

	// API v1 — authenticated surface. authMiddleware runs first so every route
	// below requires a valid Bearer JWT (→ user principal) or x-api-key (→ service
	// principal); /health + /ready above stay open. Handlers stamp the active org
	// (D-073) before any RLS-forced read.
	r.Route("/api/v1/augur", func(r chi.Router) {
		r.Use(s.authMiddleware)

		// Workloads
		r.Route("/workloads", func(r chi.Router) {
			r.Get("/", s.listWorkloads)
			r.Post("/", s.registerWorkload)
			r.Get("/{workloadID}", s.getWorkload)
			r.Get("/{workloadID}/metrics", s.getWorkloadMetrics)
			r.Post("/{workloadID}/metrics", s.ingestMetrics)
			r.Post("/{workloadID}/scale", s.scaleWorkload)
			r.Post("/{workloadID}/pause", s.pauseAutoscaling)
			r.Post("/{workloadID}/resume", s.resumeAutoscaling)
			r.Put("/{workloadID}/optimization-mode", s.setOptimizationMode)
			r.Put("/{workloadID}/scaling-bounds", s.setScalingBounds)
			r.Get("/{workloadID}/diagnose", s.diagnoseWorkload)
		})

		// Forecasts
		r.Get("/forecast/{workloadID}", s.getForecast)

		// SLO
		r.Get("/slo/{workloadID}", s.getSLOStatus)
		r.Post("/slo", s.createSLO)

		// Cost
		r.Get("/cost/report", s.getCostReport)
		r.Post("/cost/budget", s.setCostBudget)
		r.Get("/cost/idle", s.getIdleResources)

		// Alerts
		r.Get("/alerts", s.listAlerts)

		// Decisions
		r.Get("/decisions/{decisionID}", s.explainDecision)
		r.Get("/decisions", s.listDecisions)

		// Rightsizing (Phase 2)
		r.Get("/rightsizing/{workloadID}", s.getRightsizingRecommendation)
		r.Get("/rightsizing", s.getRightsizingRecommendations)
		r.Post("/rightsizing/{workloadID}/apply", s.applyRightsizing)

		// Spot management (Phase 2)
		r.Post("/spot/{workloadID}/enable", s.enableSpot)
		r.Post("/spot/{workloadID}/disable", s.disableSpot)
		r.Get("/spot/{workloadID}/status", s.getSpotStatus)

		// Decommission (Phase 2)
		r.Post("/decommission", s.decommissionResource)

		// Capacity simulation (Phase 3)
		r.Post("/simulate", s.simulateCapacity)

		// Phase 4: Multi-environment intelligence
		r.Get("/anomaly/{workloadID}/multilayer", s.detectAnomalyMultiLayer)
		r.Post("/anomaly/{workloadID}/train", s.trainAnomalyModels)
		r.Get("/ri/recommend", s.getRIRecommendation)
		r.Get("/cross-cluster/placement", s.getPlacementRecommendation)
		r.Get("/cross-cluster/instance-types", s.getInstanceTypeRecommendation)
	})

	s.router = r
}
