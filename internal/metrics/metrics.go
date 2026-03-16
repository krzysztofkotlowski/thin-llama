package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Set struct {
	Registry         *prometheus.Registry
	HTTPRequests     *prometheus.CounterVec
	HTTPRequestDur   *prometheus.HistogramVec
	ModelPulls       *prometheus.CounterVec
	ProxyFailures    *prometheus.CounterVec
}

func New() *Set {
	registry := prometheus.NewRegistry()
	set := &Set{
		Registry: registry,
		HTTPRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "thin_llama_http_requests_total",
				Help: "HTTP requests handled by thin-llama.",
			},
			[]string{"method", "path", "status"},
		),
		HTTPRequestDur: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "thin_llama_http_request_duration_seconds",
				Help:    "HTTP request latency in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),
		ModelPulls: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "thin_llama_model_pulls_total",
				Help: "Model pull attempts by status.",
			},
			[]string{"model", "status"},
		),
		ProxyFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "thin_llama_proxy_failures_total",
				Help: "Proxy failures by route.",
			},
			[]string{"route"},
		),
	}
	registry.MustRegister(set.HTTPRequests, set.HTTPRequestDur, set.ModelPulls, set.ProxyFailures)
	return set
}

func (s *Set) Handler() http.Handler {
	return promhttp.HandlerFor(s.Registry, promhttp.HandlerOpts{})
}
