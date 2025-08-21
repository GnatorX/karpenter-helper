package watchers

import (
	"context"
	"time"

	"k8s.io/client-go/kubernetes"
	klog "k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/metrics"
)

type Manager struct {
	clientset *kubernetes.Clientset
	collector *metrics.Collector

	nodeWatcher      *NodeWatcher
	podWatcher       *PodWatcher
	daemonsetWatcher *DaemonsetWatcher
}

func NewManager(clientset *kubernetes.Clientset, collector *metrics.Collector) *Manager {
	return &Manager{
		clientset: clientset,
		collector: collector,
	}
}

func (m *Manager) Start(ctx context.Context) error {
	klog.Info("Starting watcher manager")

	var err error

	m.nodeWatcher, err = NewNodeWatcher(m.clientset, m.collector)
	if err != nil {
		return err
	}

	m.podWatcher, err = NewPodWatcher(m.clientset, m.collector)
	if err != nil {
		return err
	}

	m.daemonsetWatcher, err = NewDaemonsetWatcher(m.clientset, m.collector)
	if err != nil {
		return err
	}

	go m.nodeWatcher.Run(ctx)
	go m.podWatcher.Run(ctx)
	go m.daemonsetWatcher.Run(ctx)

	// Start periodic reconciliation for detecting stale states
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			klog.Info("Stopping watcher manager")
			return nil
		case <-ticker.C:
			m.reconcileState()
		}
	}
}

func (m *Manager) reconcileState() {
	// Check for nodes that should be reaped but aren't
	if m.nodeWatcher != nil {
		m.nodeWatcher.CheckUnreapedNodes()
	}

	// Check for daemonset/nodepool mismatches
	if m.daemonsetWatcher != nil && m.nodeWatcher != nil {
		m.checkDaemonsetNodepoolMismatches()
	}
}

func (m *Manager) checkDaemonsetNodepoolMismatches() {
	// This would cross-reference daemonset pods with node nodepools
	// Implementation depends on specific Karpenter annotations/labels
}
