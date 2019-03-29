package metrics

import (
	"errors"
	_ "k8s.io/kubernetes/pkg/client/metrics/prometheus"
	_ "k8s.io/kubernetes/pkg/util/reflector/prometheus"
	_ "k8s.io/kubernetes/pkg/util/workqueue/prometheus"

	"github.com/prometheus/client_golang/prometheus"
)

// FailedCreateReason describes reason of failed instance create request
type FailedCreateReason string

// FailedDeleteReason describes reason of failed instance delete request
type FailedDeleteReason string

// FunctionLabel is a name of Controller function for which
// we measure duration
type FunctionLabel string

const (
	vmcNamespace = "vm_controller"

	DuplicateName FailedCreateReason = "failed to create VM because of VM name duplication "
	NotSure       FailedCreateReason = "failed to create VM somehow"
)

// Names of vm controller operations
const (
	Create         FunctionLabel = "createInstance"
	Delete         FunctionLabel = "deleteInstance"
	GarbageCollect FunctionLabel = "removeDanglingInstances"
)

var (
	lastGarbageCollectorExecution = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: vmcNamespace,
			Name:      "last_time_gc_executed",
			Help:      "Last time garbage collector executed",
		}, []string{"activity"},
	)

	errorsCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: vmcNamespace,
			Name:      "errors_total",
			Help:      "The number of total errors",
		}, []string{"type"},
	)

	failedCreateCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: vmcNamespace,
			Name:      "failed_create_total",
			Help:      "Number of times cloud instance create has failed.",
		}, []string{"reason"},
	)

	failedDeleteCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: vmcNamespace,
			Name:      "failed_delete_total",
			Help:      "Number of times cloud instance delete has failed.",
		}, []string{"reason"},
	)

	functionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: vmcNamespace,
			Name:      "function_duration_seconds",
			Help:      "Time taken by various functions",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1.0, 2.5, 5.0, 7.5, 10.0, 12.5, 15.0, 17.5, 20.0, 22.5, 25.0, 27.5, 30.0, 50.0, 75.0, 100.0, 1000.0},
		}, []string{"function"},
	)
)

// RegisterAll registers all metrics.
func RegisterAll() {
	prometheus.MustRegister(lastGarbageCollectorExecution)
	prometheus.MustRegister(errorsCount)
	prometheus.MustRegister(failedCreateCount)
	prometheus.MustRegister(failedDeleteCount)
	prometheus.MustRegister(functionDuration)
}

// UpdateDurationFromStart records the duration of the step identified by the
// label using start time
func UpdateDurationFromStart(label FunctionLabel, start time.Time) {
	duration := time.Now().Sub(start)
	UpdateDuration(label, duration)
}

// UpdateDuration records the duration of the step identified by the label
func UpdateDuration(label FunctionLabel, duration time.Duration) {
	functionDuration.WithLabelValues(string(label)).Observe(duration.Seconds())
}

// UpdateLastTimeGC records the time the step identified by the label was started
func UpdateLastTimeGC(label FunctionLabel, now time.Time) {
	lastGarbageCollectorExecution.WithLabelValues(string(label)).Set(float64(now.Unix()))
}

// RegisterError records any errors preventing vm controller from working.
func RegisterError(err errors) {
	errorsCount.WithLabelValues(err.Error()).Add(1.0)
}

// RegisterFailedCreate records a failed create operation
func RegisterFailedCreate(reason FailedCreateReason) {
	failedCreateCount.WithLabelValues(string(reason)).Inc()
}

// RegisterFailedDelete records a failed delete operation
func RegisterFailedDelete(reason FailedDeleteReason) {
	failedDeleteCount.WithLabelValues(string(reason)).Inc()
}
