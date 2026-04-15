/*
Copyright 2026 Fabien Dupont.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	InstanceProvisionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "nico_mapi_instance_provision_seconds",
			Help:    "Time from Create call to instance Ready state",
			Buckets: []float64{30, 60, 120, 300, 600},
		},
		[]string{"instance_type"},
	)
	APICallErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nico_mapi_api_errors_total",
			Help: "NICo API errors by method and status code",
		},
		[]string{"method", "status_code"},
	)
	MachinesManaged = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "nico_mapi_machines_managed",
			Help: "Number of machines currently managed by this provider",
		},
	)
	APILatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "nico_mapi_api_latency_seconds",
			Help:    "NICo API call latency by method",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"method"},
	)
	MachinesUnhealthy = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "nico_mapi_machines_unhealthy",
			Help: "Number of machines currently reporting critical health faults",
		},
	)
	HealthEventsIngested = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "nico_mapi_health_events_ingested_total",
			Help: "Total number of health events ingested to NICo via the fault ingestion API",
		},
	)
)

func init() {
	metrics.Registry.MustRegister(
		InstanceProvisionDuration,
		APICallErrors,
		MachinesManaged,
		APILatency,
		MachinesUnhealthy,
		HealthEventsIngested,
	)
}
