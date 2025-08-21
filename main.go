package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/metrics"
	"github.com/garvinp/karpenter-helper/pkg/watchers"
)

var (
	// kubeconfig  = flag.String("kubeconfig", "", "Path to kubeconfig file")
	metricsAddr = flag.String("metrics-addr", ":8080", "Address to serve metrics on")
)

func main() {
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	config, err := getKubeConfig()
	if err != nil {
		klog.Fatalf("Failed to get kubeconfig: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create kubernetes client: %v", err)
	}

	registry := prometheus.NewRegistry()
	metricsCollector := metrics.NewCollector()
	registry.MustRegister(metricsCollector)

	watcherManager := watchers.NewManager(clientset, metricsCollector)

	go func() {
		if err := watcherManager.Start(ctx); err != nil {
			klog.Errorf("Watcher manager failed: %v", err)
		}
	}()

	http.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	server := &http.Server{Addr: *metricsAddr}

	go func() {
		klog.Infof("Starting metrics server on %s", *metricsAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			klog.Errorf("Metrics server failed: %v", err)
		}
	}()

	<-ctx.Done()
	klog.Info("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		klog.Errorf("Server shutdown failed: %v", err)
	}
}

func getKubeConfig() (*rest.Config, error) {
	restConfig := &rest.Config{
		UserAgent: fmt.Sprintf("%s/git-%s", "test", "test123"),
	}

	restConfig.Host = fmt.Sprintf("http://kube-frontend--%s.qa.corp.stripe.com/%s", "northwest", "mspdev")

	httpTransport := &http.Transport{}
	certproxyPath := path.Join(os.Getenv("HOME"), ".stripeproxy")
	if _, err := os.Stat(certproxyPath); err == nil {
		httpTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("unix", certproxyPath)
		}
	} else {
		log.Fatal("Unable to locate Stripe's cert-proxy in laptop mode")
	}
	restConfig.Transport = httpTransport
	return restConfig, nil
	// if *kubeconfig != "" {
	// 	return clientcmd.BuildConfigFromFlags("", *kubeconfig)
	// }

	// if config, err := rest.InClusterConfig(); err == nil {
	// 	return config, nil
	// }

	// kubeconfig := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
	// return clientcmd.BuildConfigFromFlags("", kubeconfig)
}
