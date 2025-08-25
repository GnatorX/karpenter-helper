package manager

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	klog "k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/informers"
	"github.com/garvinp/karpenter-helper/pkg/metrics"
	"github.com/garvinp/karpenter-helper/pkg/processors"
)

type Manager struct {
	clientset *kubernetes.Clientset
	collector *metrics.Collector

	nodeInformer      *informers.NodeInformer
	podInformer       *informers.PodInformer
	daemonsetInformer *informers.DaemonsetInformer
	nodepoolInformer  *informers.NodePoolInformer
	nodepoolProcessor *processors.NodePoolDaemonsetProcessor
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

	m.nodeInformer, err = informers.NewNodeInformer(ctx, m.clientset, m.collector)
	if err != nil {
		return err
	}

	m.podInformer, err = informers.NewPodInformer(ctx, m.clientset, m.collector)
	if err != nil {
		return err
	}

	m.daemonsetInformer, err = informers.NewDaemonsetInformer(ctx, m.clientset, m.collector)
	if err != nil {
		return err
	}

	m.nodepoolInformer, err = informers.NewNodePoolInformer(ctx, m.clientset, m.collector)
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
	if err := m.nodepoolInformer.Start(ctx); err != nil {
		return err
	}

	// Wait for all informers to sync
	klog.Info("Waiting for informers to sync...")
	if !cache.WaitForCacheSync(ctx.Done(),
		m.nodeInformer.HasSynced,
		m.podInformer.HasSynced,
		m.daemonsetInformer.HasSynced,
		m.nodepoolInformer.HasSynced) {
		return fmt.Errorf("failed to sync informers")
	}
	klog.Info("All informers synced successfully")

	// Create the processor with all informers
	m.nodepoolProcessor = processors.NewNodePoolDaemonsetProcessor(m.collector, m.nodeInformer, m.podInformer, m.daemonsetInformer, m.nodepoolInformer)

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

	// Check for daemonset/nodepool mismatches using the processor
	if m.nodepoolProcessor != nil {
		m.nodepoolProcessor.CheckForMismatches()
	}

	// Check for NodePool-related issues
	if m.nodepoolInformer != nil {
		m.checkNodePoolIssues()
	}
}

func (m *Manager) checkNodePoolIssues() {
	// Check for NodePool-related issues
	// This could include:
	// - NodePools with invalid configurations
	// - NodePools that are not scaling properly
	// - TTL-related issues
	if m.nodepoolInformer != nil {
		nodepools := m.nodepoolInformer.ListNodePools()
		klog.V(2).Infof("Checking %d NodePools for issues", len(nodepools))

		for _, nodepool := range nodepools {
			// Check for TTL configuration issues
			if nodepool.ExpectedTTL != nil {
				klog.V(3).Infof("NodePool %s has TTL configured: %v", nodepool.Name, *nodepool.ExpectedTTL)
			}

			// Check for other potential issues
			// This is a placeholder for future NodePool health checks
		}
	}
}
