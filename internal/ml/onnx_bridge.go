package ml

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// ONNXBridge manages ONNX model loading, inference, and the model registry.
// Phase 4: Initially a no-op bridge that falls back to Go implementations.
// When onnxruntime_go is available, models are loaded from the registry.
type ONNXBridge struct {
	modelRepo *postgres.ModelRegistryRepo
	available bool // true when ONNX runtime is initialized
}

func NewONNXBridge(modelRepo *postgres.ModelRegistryRepo) *ONNXBridge {
	// In production: attempt to initialize onnxruntime_go here
	// For now: ONNX is unavailable, all inference uses Go fallbacks
	return &ONNXBridge{
		modelRepo: modelRepo,
		available: false,
	}
}

// IsAvailable returns true when ONNX runtime is loaded
func (b *ONNXBridge) IsAvailable() bool {
	return b.available
}

// RegisterModel adds a new model version to the registry
func (b *ONNXBridge) RegisterModel(ctx context.Context, model *domain.ModelRegistry) error {
	model.ID = uuid.New()
	model.CreatedAt = time.Now()
	return b.modelRepo.Create(ctx, model)
}

// GetActiveModel returns the currently active model for a given type
func (b *ONNXBridge) GetActiveModel(ctx context.Context, modelType domain.ForecastModel) (*domain.ModelRegistry, error) {
	return b.modelRepo.FindActive(ctx, string(modelType))
}

// InferIsolationForest runs anomaly scoring using ONNX Isolation Forest model.
// Returns anomaly score (0-1). Falls back to Go implementation when ONNX unavailable.
func (b *ONNXBridge) InferIsolationForest(features []float64) (float64, error) {
	if !b.available {
		return 0, fmt.Errorf("ONNX runtime not available — use Go fallback")
	}

	// In production: load ONNX model, create tensor, run inference
	// session.Run([]ort.Value{inputTensor}, outputNames)
	logger.Debug("ONNX Isolation Forest inference: %d features", len(features))
	return 0.5, nil
}

// InferAutoencoder runs anomaly scoring using ONNX Autoencoder model.
// Returns reconstruction error (higher = more anomalous).
func (b *ONNXBridge) InferAutoencoder(features []float64) (float64, error) {
	if !b.available {
		return 0, fmt.Errorf("ONNX runtime not available — use Go fallback")
	}

	logger.Debug("ONNX Autoencoder inference: %d features", len(features))
	return 0.1, nil
}

// InferNHiTS runs demand forecasting using ONNX N-HiTS model.
// Returns predicted values for the forecast horizon.
func (b *ONNXBridge) InferNHiTS(history []float64, horizonSteps int) ([]float64, error) {
	if !b.available {
		return nil, fmt.Errorf("ONNX runtime not available — use Holt-Winters fallback")
	}

	logger.Debug("ONNX N-HiTS inference: %d history points, %d horizon", len(history), horizonSteps)
	forecasts := make([]float64, horizonSteps)
	return forecasts, nil
}
