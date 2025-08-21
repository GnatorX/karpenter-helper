local karpenterIssues = import 'karpenter-issues.libsonnet';

karpenterIssues {
  _config+:: {
    // Configuration options
    karpenterHelperJobName: 'karpenter-helper',
    karpenterHelperSelector: 'job="%s"' % self.karpenterHelperJobName,
    
    // Grafana dashboard configuration
    grafanaK8s+:: {
      dashboardNamePrefix: 'Karpenter / ',
      dashboardTags: ['karpenter', 'kubernetes'],
    },
    
    // Alert configuration  
    alerting+:: {
      daemonsetMismatchThreshold: 5, // alerts if >5 mismatches per hour
      ttlDeathRateThreshold: 0.1,    // alerts if TTL death rate > 0.1/min
      nominationFailureThreshold: 10, // alerts if >10 nomination failures per hour
      reapingDelayThreshold: 300,     // alerts if reaping delayed >5min
    },
  },
}