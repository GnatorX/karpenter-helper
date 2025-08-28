package manager

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	kinformers "k8s.io/client-go/informers"
	klog "k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/informers"
	"github.com/garvinp/karpenter-helper/pkg/metrics"
	"github.com/garvinp/karpenter-helper/pkg/processors"
)

type Manager struct {
	clientset *kubernetes.Clientset
	config    *rest.Config
	collector *metrics.Collector

	nodeInformer      *informers.NodeInformer
	podInformer       *informers.PodInformer
	daemonsetInformer *informers.DaemonsetInformer
	nodepoolInformer  *informers.NodePoolInformer
	nodepoolProcessor *processors.NodePoolDaemonsetProcessor
	stopCh            chan struct{}
}

func NewManager(clientset *kubernetes.Clientset, config *rest.Config, collector *metrics.Collector) *Manager {
	return &Manager{
		clientset: clientset,
		config:    config,
		collector: collector,
		stopCh:    make(chan struct{}),
	}
}

func (m *Manager) Start(ctx context.Context) error {
	klog.Info("Starting informer manager")

	var err error

	// Create dynamic client for Karpenter resources
	// We need to get the config from the clientset
	// config := m.clientset.RESTClient().GetConfig() // This line is removed as per the edit hint
	dynamicClient, err := dynamic.NewForConfig(m.config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Create dynamic informer factory
	dynamicInformerFactory := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, 0)

	sharedInformerFactory := kinformers.NewSharedInformerFactory(m.clientset, 0)

	m.nodeInformer, err = informers.NewNodeInformer(ctx, sharedInformerFactory, m.collector)
	if err != nil {
		return err
	}

	m.podInformer, err = informers.NewPodInformer(ctx, sharedInformerFactory, m.collector)
	if err != nil {
		return err
	}

	m.daemonsetInformer, err = informers.NewDaemonsetInformer(ctx, sharedInformerFactory, m.collector)
	if err != nil {
		return err
	}

	m.nodepoolInformer, err = informers.NewNodePoolInformer(ctx, dynamicInformerFactory, m.collector)
	if err != nil {
		return err
	}

	// Start both informer factories
	sharedInformerFactory.Start(m.stopCh)
	dynamicInformerFactory.Start(m.stopCh)

	// Wait for all informers to sync
	klog.Info("Waiting for informers to sync...")
	synced := sharedInformerFactory.WaitForCacheSync(ctx.Done())
	for informerType, synced := range synced {
		if !synced {
			return fmt.Errorf("failed to sync informers: %v", informerType)
		}
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
