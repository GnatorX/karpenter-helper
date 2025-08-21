package watchers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	klog "k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/metrics"
)

type Manager struct {
	clientset *kubernetes.Clientset
	collector *metrics.Collector

	nodeInformer      *NodeInformer
	podInformer       *PodInformer
	daemonsetInformer *DaemonsetInformer
	stopCh            chan struct{}
	wg                sync.WaitGroup
}

func NewManager(clientset *kubernetes.Clientset, collector *metrics.Collector) *Manager {
	return &Manager{
		clientset: clientset,
		collector: collector,
		stopCh:    make(chan struct{}),
	}
}

func (m *Manager) Start(ctx context.Context) error {
	klog.Info("Starting informer manager")

	var err error

	m.nodeInformer, err = NewNodeInformer(m.clientset, m.collector)
	if err != nil {
		return err
	}

	m.podInformer, err = NewPodInformer(m.clientset, m.collector)
	if err != nil {
		return err
	}

	m.daemonsetInformer, err = NewDaemonsetInformer(m.clientset, m.collector)
	if err != nil {
		return err
	}

	// Start all informers
	if err := m.nodeInformer.Start(ctx); err != nil {
		return err
	}
	if err := m.podInformer.Start(ctx); err != nil {
		return err
	}
	if err := m.daemonsetInformer.Start(ctx); err != nil {
		return err
	}

	// Wait for all informers to sync
	klog.Info("Waiting for informers to sync...")
	if !cache.WaitForCacheSync(ctx.Done(),
		m.nodeInformer.HasSynced,
		m.podInformer.HasSynced,
		m.daemonsetInformer.HasSynced) {
		return fmt.Errorf("failed to sync informers")
	}
	klog.Info("All informers synced successfully")

	// Start periodic reconciliation for detecting stale states
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			klog.Info("Stopping informer manager")
			return nil
		case <-ticker.C:
			m.reconcileState()
		}
	}
}

func (m *Manager) reconcileState() {
	// Check for nodes that should be reaped but aren't
	if m.nodeInformer != nil {
		m.nodeInformer.CheckUnreapedNodes()
	}

	// Check for daemonset/nodepool mismatches
	if m.daemonsetInformer != nil && m.nodeInformer != nil {
		m.checkDaemonsetNodepoolMismatches()
	}
}

func (m *Manager) checkDaemonsetNodepoolMismatches() {
	// This would cross-reference daemonset pods with node nodepools
	// Implementation depends on specific Karpenter annotations/labels
}
