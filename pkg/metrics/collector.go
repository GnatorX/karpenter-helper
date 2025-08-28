package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	namespace = "karpenter_helper"
)

type Collector struct {
	mu sync.RWMutex

	// Daemonset mismatch metrics
	daemonsetNodepoolMismatch *prometheus.CounterVec

	// Node TTL death metrics
	nodeTTLDeaths *prometheus.CounterVec

	// Nominated pod failure metrics
	nominatedPodFailures         *prometheus.CounterVec
	nodesCreatedForNominatedPods *prometheus.CounterVec

	// Node reaping issues
	nodesNotReapedDueToNomination *prometheus.GaugeVec
	nodeReapingDelaySeconds       *prometheus.HistogramVec
}

func NewCollector() *Collector {
	return &Collector{
		daemonsetNodepoolMismatch: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "daemonset_nodepool_mismatch_total",
				Help:      "Number of daemonset pods scheduled on nodes that don't match their nodepool selectors",
			},
			[]string{"daemonset", "namespace", "node", "nodepool"},
		),

		nodeTTLDeaths: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "node_ttl_deaths_total",
				Help:      "Number of nodes deleted due to TTL expiration",
			},
			[]string{"node", "nodepool", "ttl_reason"},
		),

		nominatedPodFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "nominated_pod_failures_total",
				Help:      "Number of times a nominated pod failed to land on its created node",
			},
			[]string{"pod", "namespace", "node", "reason"},
		),

		nodesCreatedForNominatedPods: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "nodes_created_for_nominated_pods_total",
				Help:      "Number of nodes created for nominated pods",
			},
			[]string{"node", "pod", "namespace", "nodepool"},
		),

		nodesNotReapedDueToNomination: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "nodes_not_reaped_due_to_nomination",
				Help:      "Number of nodes that should be reaped but aren't due to nominated pods",
			},
			[]string{"node", "nodepool", "nominated_pod", "nominated_namespace"},
		),

		nodeReapingDelaySeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "node_reaping_delay_seconds",
				Help:      "Time delay between when a node should be reaped and when it actually gets reaped",
				Buckets:   []float64{30, 60, 120, 300, 600, 1200, 3600},
			},
			[]string{"node", "nodepool", "delay_reason"},
		),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	c.daemonsetNodepoolMismatch.Describe(ch)
	c.nodeTTLDeaths.Describe(ch)
	c.nominatedPodFailures.Describe(ch)
	c.nodesCreatedForNominatedPods.Describe(ch)
	c.nodesNotReapedDueToNomination.Describe(ch)
	c.nodeReapingDelaySeconds.Describe(ch)
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	c.daemonsetNodepoolMismatch.Collect(ch)
	c.nodeTTLDeaths.Collect(ch)
	c.nominatedPodFailures.Collect(ch)
	c.nodesCreatedForNominatedPods.Collect(ch)
	c.nodesNotReapedDueToNomination.Collect(ch)
	c.nodeReapingDelaySeconds.Collect(ch)
}

// Metric recording methods
func (c *Collector) RecordDaemonsetNodepoolMismatch(daemonset, namespace, node, nodepool string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.daemonsetNodepoolMismatch.WithLabelValues(daemonset, namespace, node, nodepool).Inc()
}

func (c *Collector) RecordNodeTTLDeath(node, nodepool, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodeTTLDeaths.WithLabelValues(node, nodepool, reason).Inc()
}

func (c *Collector) RecordNominatedPodFailure(pod, namespace, node, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nominatedPodFailures.WithLabelValues(pod, namespace, node, reason).Inc()
}

func (c *Collector) RecordNodeCreatedForNominatedPod(node, pod, namespace, nodepool string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodesCreatedForNominatedPods.WithLabelValues(node, pod, namespace, nodepool).Inc()
}

func (c *Collector) SetNodeNotReapedDueToNomination(node, nodepool, nominatedPod, nominatedNamespace string, count float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodesNotReapedDueToNomination.WithLabelValues(node, nodepool, nominatedPod, nominatedNamespace).Set(count)
}

func (c *Collector) RecordNodeReapingDelay(node, nodepool, reason string, delaySeconds float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodeReapingDelaySeconds.WithLabelValues(node, nodepool, reason).Observe(delaySeconds)
}
