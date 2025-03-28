// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	addonNamespace                          string = "open-cluster-management-agent-addon"
	kubeconfigFilename                      string = "../../kubeconfig_cluster"
	loggingLevelAnnotation                  string = "log-level=8"
	evaluationConcurrencyAnnotation         string = "policy-evaluation-concurrency=5"
	clientQPSAnnotation                     string = "client-qps=50"
	prometheusEnabledAnnotation             string = "prometheus-metrics-enabled=true"
	opPolicyEnabledAnnotation               string = "operator-policy-disabled=false"
	addOnDeploymentConfigCR                 string = "../resources/addondeploymentconfig.yaml"
	addOnDeploymentConfigWithCustomVarsCR   string = "../resources/addondeploymentconfig_customvars.yaml"
	addOnDeploymentConfigWithAgentInstallNs string = "../resources/addondeploymentconfig_agentInstallNs.yaml"
	agentInstallNs                          string = "test-install-ns"
)

var (
	gvrDeployment          schema.GroupVersionResource
	gvrPod                 schema.GroupVersionResource
	gvrNamespace           schema.GroupVersionResource
	gvrManagedClusterAddOn schema.GroupVersionResource
	gvrManagedCluster      schema.GroupVersionResource
	gvrManifestWork        schema.GroupVersionResource
	gvrSecret              schema.GroupVersionResource
	gvrServiceMonitor      schema.GroupVersionResource
	gvrService             schema.GroupVersionResource
	gvrClusterRole         schema.GroupVersionResource
	gvrRoleBinding         schema.GroupVersionResource
	gvrPolicyCrd           schema.GroupVersionResource
	managedClusterList     []managedClusterConfig
	clientDynamic          dynamic.Interface
	hubKubeconfigInternal  []byte
)

type managedClusterConfig struct {
	clusterName   string
	clusterClient dynamic.Interface
	clusterType   string
	// Only relevant for hosted mode tests.
	hostedOnHub bool
	kubeconfig  []byte
}

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "governance policy addon controller e2e Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	gvrDeployment = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	gvrPod = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	gvrNamespace = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
	gvrManagedClusterAddOn = schema.GroupVersionResource{
		Group: "addon.open-cluster-management.io", Version: "v1alpha1", Resource: "managedclusteraddons",
	}
	gvrManagedCluster = schema.GroupVersionResource{
		Group: "cluster.open-cluster-management.io", Version: "v1", Resource: "managedclusters",
	}
	gvrManifestWork = schema.GroupVersionResource{
		Group: "work.open-cluster-management.io", Version: "v1", Resource: "manifestworks",
	}
	gvrSecret = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
	gvrServiceMonitor = schema.GroupVersionResource{
		Group: "monitoring.coreos.com", Version: "v1", Resource: "servicemonitors",
	}
	gvrService = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
	gvrClusterRole = schema.GroupVersionResource{
		Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles",
	}
	gvrRoleBinding = schema.GroupVersionResource{
		Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings",
	}
	gvrPolicyCrd = schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}
	clientDynamic = NewKubeClientDynamic("", kubeconfigFilename+"1_e2e", "")

	var err error
	hubKubeconfigInternal, err = os.ReadFile(kubeconfigFilename + "1_e2e-internal")
	Expect(err).ToNot(HaveOccurred())

	managedClusterList = getManagedClusters(ctx, clientDynamic)
})

func getManagedClusters(ctx context.Context, client dynamic.Interface) []managedClusterConfig {
	clusterObjs, err := client.Resource(gvrManagedCluster).List(ctx, metav1.ListOptions{})
	if err != nil {
		panic(err)
	}

	var clusters []managedClusterConfig

	for i, cluster := range clusterObjs.Items {
		clusterName, _, err := unstructured.NestedString(cluster.Object, "metadata", "name")
		if err != nil {
			panic(err)
		}

		kubeconfigPath := fmt.Sprintf("%s%d_e2e", kubeconfigFilename, i+1)

		clusterClient := NewKubeClientDynamic("", kubeconfigPath, "")

		kubeconfigContents, err := os.ReadFile(kubeconfigPath + "-internal")
		if err != nil {
			panic(err)
		}

		var clusterType string
		if i == 0 {
			clusterType = "hub"
		} else {
			clusterType = "managed"
		}

		newCluster := managedClusterConfig{
			clusterName,
			clusterClient,
			clusterType,
			false,
			kubeconfigContents,
		}
		clusters = append(clusters, newCluster)
	}

	return clusters
}

func NewKubeClientDynamic(url, kubeconfig, context string) dynamic.Interface {
	config, err := LoadConfig(url, kubeconfig, context)
	if err != nil {
		panic(err)
	}

	clientset, err := dynamic.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	return clientset
}

func LoadConfig(url, kubeconfig, context string) (*rest.Config, error) {
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	// If we have an explicit indication of where the kubernetes config lives, read that.
	if kubeconfig != "" {
		if context == "" {
			return clientcmd.BuildConfigFromFlags(url, kubeconfig)
		}

		return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
			&clientcmd.ConfigOverrides{
				CurrentContext: context,
			}).ClientConfig()
	}

	// If not, try the in-cluster config.
	if c, err := rest.InClusterConfig(); err == nil {
		return c, nil
	}

	// If no in-cluster config, try the default location in the user's home directory.
	if usr, err := user.Current(); err == nil {
		if c, err := clientcmd.BuildConfigFromFlags("", filepath.Join(usr.HomeDir, ".kube", "config")); err == nil {
			return c, nil
		}
	}

	return nil, errors.New("could not create a valid kubeconfig")
}
