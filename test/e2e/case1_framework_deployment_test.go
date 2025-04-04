// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

const (
	case1ManagedClusterAddOnName         string = "governance-policy-framework"
	case1ManagedClusterAddOnCR           string = "../resources/framework_addon_cr.yaml"
	case1ClusterManagementAddOnCRDefault string = "../resources/framework_clustermanagementaddon.yaml"
	case1ClusterManagementAddOnCR        string = "../resources/framework_clustermanagementaddon_config.yaml"
	case1CMAAddonWithInstallNs           string = "../resources/framework_cma_config_agentInstallNs.yaml"
	case1hubAnnotationMCAOCR             string = "../resources/framework_hub_annotation_addon_cr.yaml"
	case1hubValuesMCAOCR                 string = "../resources/framework_hub_values_addon_cr.yaml"
	case1DeploymentName                  string = "governance-policy-framework"
	case1PodSelector                     string = "app=governance-policy-framework"
	case1MWName                          string = "addon-governance-policy-framework-deploy-0"
	case1MWPatch                         string = "../resources/manifestwork_add_patch.json"
	ocmPolicyNs                          string = "open-cluster-management-policies"
)

var _ = Describe("Test framework deployment", Ordered, func() {
	BeforeAll(func() {
		By("Deploying the default governance-policy-framework ClusterManagementAddon to the hub cluster")
		Kubectl("apply", "-f", case1ClusterManagementAddOnCRDefault)
	})

	AfterAll(func() {
		if CurrentSpecReport().Failed() {
			debugCollection(case1PodSelector)
		}

		By("Deleting the default governance-policy-framework ClusterManagementAddon from the hub cluster")
		Kubectl("delete", "-f", case1ClusterManagementAddOnCRDefault)
	})

	It("should create the framework deployment in hosted mode in user's custom namespace", func(ctx SpecContext) {
		By("Creating the AddOnDeploymentConfig")
		Kubectl("apply", "-f", addOnDeploymentConfigWithAgentInstallNs)
		DeferCleanup(func() {
			By("Delete the AddOnDeploymentConfig")
			Kubectl("delete", "-f", addOnDeploymentConfigWithAgentInstallNs)
		})

		By("Applying the framework ClusterManagementAddOn to use the AddOnDeploymentConfig")
		Kubectl("apply", "-f", case1CMAAddonWithInstallNs)
		DeferCleanup(func() {
			By("Apply Default ClusterManagementAdd")
			Kubectl("apply", "-f", case1ClusterManagementAddOnCRDefault)
		})

		for _, cluster := range managedClusterList[1:] {
			logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "
			By(logPrefix + "deploying the default framework managedclusteraddon")
			Kubectl("apply", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR)

			By("Addon should be installed in " + agentInstallNs)
			deploy := GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, agentInstallNs, true, 60,
			)
			Expect(deploy).NotTo(BeNil())

			By(logPrefix +
				"removing the framework deployment when the ManagedClusterAddOn CR is removed")
			Kubectl("delete", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "--timeout=180s")
			deploy = GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, agentInstallNs, false, 180,
			)
			Expect(deploy).To(BeNil())

			opts := metav1.ListOptions{
				LabelSelector: case1PodSelector,
			}
			pods := ListWithTimeoutByNamespace(ctx, cluster.clusterClient, gvrPod, opts, agentInstallNs, 0, false, 180)
			Expect(pods).To(BeNil())

			By("Should not have " + ocmPolicyNs + " in hosted mode")
			GetWithTimeout(
				ctx, cluster.clusterClient, gvrNamespace, ocmPolicyNs, "", false, 60,
			)
		}
	})

	It("should create the default framework deployment on separate managed clusters", func(ctx SpecContext) {
		for i, cluster := range managedClusterList[1:] {
			Expect(cluster.clusterType).To(Equal("managed"))

			logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "
			By(logPrefix + "deploying the default framework managedclusteraddon")
			Kubectl("apply", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR)
			deploy := GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 60,
			)
			Expect(deploy).NotTo(BeNil())

			// Use i+1 since the for loop ranges over a slice skipping first index
			checkContainersAndAvailability(ctx, cluster, i+1)

			expectedArgs := []string{
				"--cluster-namespace=" + cluster.clusterName, "--leader-elect=false",
				"--evaluation-concurrency=2", "--client-max-qps=30", "--client-burst=45",
			}

			checkArgs(ctx, cluster, expectedArgs...)

			By(logPrefix + "removing the framework deployment when the ManagedClusterAddOn CR is removed")
			Kubectl("delete", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "--timeout=90s")
			deploy = GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, false, 30,
			)
			Expect(deploy).To(BeNil())
		}
	})

	It("should create the default framework deployment in hosted mode", Label("hosted-mode"), func(ctx SpecContext) {
		for i, cluster := range managedClusterList[1:] {
			Expect(cluster.clusterType).To(Equal("managed"))

			cluster = managedClusterConfig{
				clusterClient: cluster.clusterClient,
				clusterName:   cluster.clusterName,
				clusterType:   cluster.clusterType,
				hostedOnHub:   true,
			}
			hubClusterConfig := managedClusterList[0]
			hubClient := hubClusterConfig.clusterClient
			installNamespace := cluster.clusterName + "-hosted"

			logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "

			By(logPrefix + "setting the vendor label to OpenShift")
			Kubectl(
				"label",
				"managedcluster",
				cluster.clusterName,
				"vendor=OpenShift",
				"--overwrite",
				fmt.Sprintf("--kubeconfig=%s1_e2e", kubeconfigFilename),
			)

			DeferCleanup(func() {
				By(logPrefix + " removing the vendor label")
				Kubectl(
					"label",
					"managedcluster",
					cluster.clusterName,
					"vendor-",
					fmt.Sprintf("--kubeconfig=%s1_e2e", kubeconfigFilename),
				)
			})

			installAddonInHostedMode(
				ctx, logPrefix, hubClient, case1ManagedClusterAddOnName,
				cluster.clusterName, hubClusterConfig.clusterName, installNamespace, map[string]string{
					"addon.open-cluster-management.io/on-multicluster-hub": "true",
				})

			// Use i+1 since the for loop ranges over a slice skipping first index
			checkContainersAndAvailability(ctx, cluster, i+1)

			checkArgs(
				ctx,
				cluster,
				"--cluster-namespace="+installNamespace,
				"--cluster-namespace-on-hub="+cluster.clusterName,
			)

			By("Test policy crd annotation when management + hub hosted mode")
			crd, err := clientDynamic.Resource(gvrPolicyCrd).Get(
				ctx, policyCrdName, metav1.GetOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			_, ok := crd.GetAnnotations()[deletionOrphanAnnotationKey]
			Expect(ok).Should(BeTrue())

			deleteCtx, deleteCancel := context.WithTimeout(ctx, 15*time.Second)
			defer deleteCancel()

			By(logPrefix + "removing the framework deployment when the ManagedClusterAddOn CR is removed")
			err = hubClient.Resource(gvrManagedClusterAddOn).Namespace(cluster.clusterName).Delete(
				deleteCtx, case1ManagedClusterAddOnName, metav1.DeleteOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			deploy := GetWithTimeout(ctx, hubClient, gvrDeployment, case1DeploymentName, installNamespace, false, 90)
			Expect(deploy).To(BeNil())

			namespace := GetWithTimeout(ctx, hubClient, gvrNamespace, installNamespace, "", false, 120)
			Expect(namespace).To(BeNil())
		}
	})

	It("should create the default framework deployment in hosted mode in klusterlet agent namespace",
		Label("hosted-mode"), func(ctx SpecContext) {
			By("Creating the AddOnDeploymentConfig")
			Kubectl("apply", "-f", addOnDeploymentConfigWithCustomVarsCR)
			By("Applying the governance-policy-framework ClusterManagementAddOn to use the AddOnDeploymentConfig")
			Kubectl("apply", "-f", case1ClusterManagementAddOnCR)

			for i, cluster := range managedClusterList[1:] {
				Expect(cluster.clusterType).To(Equal("managed"))

				cluster = managedClusterConfig{
					clusterClient: cluster.clusterClient,
					clusterName:   cluster.clusterName,
					clusterType:   cluster.clusterType,
					hostedOnHub:   true,
				}
				hubClusterConfig := managedClusterList[0]
				hubClient := hubClusterConfig.clusterClient
				installNamespace := cluster.clusterName + "-hosted"

				logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "

				installAddonInHostedMode(
					ctx, logPrefix, hubClient, case1ManagedClusterAddOnName,
					cluster.clusterName, hubClusterConfig.clusterName, installNamespace, nil)

				// Use i+1 since the for loop ranges over a slice skipping first index
				checkContainersAndAvailabilityInNamespace(ctx, cluster, i+1, installNamespace)

				deleteCtx, deleteCancel := context.WithTimeout(ctx, 15*time.Second)
				defer deleteCancel()

				By(logPrefix + "verifying removing the framework deployment when the ManagedClusterAddOn CR is removed")
				err := hubClient.Resource(gvrManagedClusterAddOn).Namespace(cluster.clusterName).Delete(
					deleteCtx, case1ManagedClusterAddOnName, metav1.DeleteOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				deploy := GetWithTimeout(
					ctx, hubClient, gvrDeployment, case1DeploymentName, installNamespace, false, 90)
				Expect(deploy).To(BeNil())

				By(logPrefix + "verifying install namespace is not removed when the ManagedClusterAddOn CR is removed")
				namespace := GetWithTimeout(ctx, hubClient, gvrNamespace, installNamespace, "", true, 30)
				Expect(namespace).NotTo(BeNil())

				ctxNS, cancelNS := context.WithTimeout(ctx, 15*time.Second)
				defer cancelNS()

				By(logPrefix + "Cleaning up the install namespace")
				err = hubClient.Resource(gvrNamespace).Delete(
					ctxNS, installNamespace, metav1.DeleteOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				namespace = GetWithTimeout(ctx, hubClient, gvrNamespace, installNamespace, "", false, 120)
				Expect(namespace).To(BeNil())
			}
			By("Deleting the AddOnDeploymentConfig")
			Kubectl("delete", "-f", addOnDeploymentConfigWithCustomVarsCR, "--timeout=15s")
			By("Restoring the governance-policy-framework ClusterManagementAddOn")
			Kubectl("apply", "-f", case1ClusterManagementAddOnCRDefault)
		})

	It("should create a framework deployment with customizations", func(ctx SpecContext) {
		for i, cluster := range managedClusterList {
			logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "
			By(logPrefix + "deploying the default framework managedclusteraddon")
			Kubectl("apply", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR)

			deploy := GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 60,
			)
			Expect(deploy).NotTo(BeNil())

			checkContainersAndAvailability(ctx, cluster, i)

			By(logPrefix + "annotating the managedclusteraddon with the " + loggingLevelAnnotation + " annotation")
			Kubectl("annotate", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, loggingLevelAnnotation)

			By(
				logPrefix + "annotating the managedclusteraddon with the " + evaluationConcurrencyAnnotation +
					" annotation",
			)
			Kubectl("annotate", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR,
				evaluationConcurrencyAnnotation)

			By(logPrefix + "annotating the managedclusteraddon with the " + clientQPSAnnotation + " annotation")
			Kubectl("annotate", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, clientQPSAnnotation)

			checkArgs(ctx, cluster, "--log-encoder=console", "--log-level=8", "--v=6",
				"--evaluation-concurrency=5", "--client-max-qps=50")

			By(logPrefix + "removing the framework deployment when the ManagedClusterAddOn CR is removed")
			Kubectl("delete", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "--timeout=90s")
			deploy = GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, false, 30,
			)
			Expect(deploy).To(BeNil())

			By("Should have " + ocmPolicyNs + " in normal mode")
			ns := GetWithTimeout(
				ctx, cluster.clusterClient, gvrNamespace, ocmPolicyNs, "", true, 60,
			)
			Expect(ns).ShouldNot(BeNil())
		}
	})

	It("should create a framework deployment with node selector on the managed cluster", func(ctx SpecContext) {
		By("Creating the AddOnDeploymentConfig")
		Kubectl("apply", "-f", addOnDeploymentConfigCR)
		By("Applying the governance-policy-framework ClusterManagementAddOn to use the AddOnDeploymentConfig")
		Kubectl("apply", "-f", case1ClusterManagementAddOnCR)

		for i, cluster := range managedClusterList {
			logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "
			By(logPrefix + "deploying the default framework managedclusteraddon")
			Kubectl("apply", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR)

			deploy := GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 60,
			)
			Expect(deploy).NotTo(BeNil())

			checkContainersAndAvailability(ctx, cluster, i)

			deploy = GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 30,
			)
			Expect(deploy).NotTo(BeNil())

			By(logPrefix + "verifying the nodeSelector")
			nodeSelector, _, _ := unstructured.NestedStringMap(
				deploy.Object, "spec", "template", "spec", "nodeSelector",
			)
			Expect(nodeSelector).To(Equal(map[string]string{"kubernetes.io/os": "linux"}))

			By(logPrefix + "verifying the tolerations")
			tolerations, _, _ := unstructured.NestedSlice(deploy.Object, "spec", "template", "spec", "tolerations")
			Expect(tolerations).To(HaveLen(1))
			expected := map[string]interface{}{
				"key":      "dedicated",
				"operator": "Equal",
				"value":    "something-else",
				"effect":   "NoSchedule",
			}
			Expect(tolerations[0]).To(Equal(expected))

			By(logPrefix + "removing the framework deployment when the ManagedClusterAddOn CR is removed")
			Kubectl("delete", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "--timeout=90s")
			deploy = GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, false, 30,
			)
			Expect(deploy).To(BeNil())
		}

		By("Deleting the AddOnDeploymentConfig")
		Kubectl("delete", "-f", addOnDeploymentConfigCR, "--timeout=15s")
		By("Restoring the governance-policy-framework ClusterManagementAddOn")
		Kubectl("apply", "-f", case1ClusterManagementAddOnCRDefault)
	})

	It("should create a framework deployment with resource requirements on the managed cluster",
		func(ctx SpecContext) {
			deploymentConfigTests := map[string]map[string]interface{}{
				"../resources/addondeploymentconfig_empty.yaml": {
					"requests": map[string]interface{}{"memory": "64Mi"},
					"limits":   map[string]interface{}{"memory": "512Mi"},
				},
				"../resources/addondeploymentconfig_resourceRequirements_individual.yaml": {
					"requests": map[string]interface{}{"memory": "50Mi"},
					"limits":   map[string]interface{}{"memory": "100Mi"},
				},
				"../resources/addondeploymentconfig_resourceRequirements_reduced.yaml": {
					"requests": map[string]interface{}{"memory": "32Mi"},
					"limits":   map[string]interface{}{"memory": "128Mi"},
				},
				"../resources/addondeploymentconfig_resourceRequirements_universal.yaml": {
					"requests": map[string]interface{}{"memory": "512Mi"},
					"limits":   map[string]interface{}{"memory": "1Gi"},
				},
			}

			for configFile, expected := range deploymentConfigTests {
				By("Creating the AddOnDeploymentConfig")
				Kubectl("apply", "-f", configFile)
				By("Applying the governance-policy-framework ClusterManagementAddOn to use the AddOnDeploymentConfig")
				Kubectl("apply", "-f", case1ClusterManagementAddOnCR)

				for i, cluster := range managedClusterList {
					logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "
					By(logPrefix + "deploying the default framework managedclusteraddon")
					Kubectl("apply", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR)

					deploy := GetWithTimeout(
						ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 60,
					)
					Expect(deploy).NotTo(BeNil())

					checkContainersAndAvailability(ctx, cluster, i)

					By(logPrefix + "verifying the resources")
					Eventually(func(g Gomega) {
						deploy := GetWithTimeout(
							ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 30,
						)
						g.Expect(deploy).NotTo(BeNil())
						containerSlice, _, err := unstructured.NestedSlice(
							deploy.Object, "spec", "template", "spec", "containers")
						g.Expect(err).NotTo(HaveOccurred())
						g.Expect(containerSlice).To(HaveLen(1))
						container, ok := containerSlice[0].(map[string]interface{})
						g.Expect(ok).To(BeTrue(), "Deployment container should be a map[string]interface{}")
						resources, _, _ := unstructured.NestedMap(container, "resources")
						g.Expect(resources).To(Equal(expected))
					}, 30, 1).Should(Succeed())
				}
			}

			for _, cluster := range managedClusterList {
				logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "
				By(logPrefix + "removing the framework deployment when the ManagedClusterAddOn CR is removed")
				Kubectl("delete", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "--timeout=90s")
				deploy := GetWithTimeout(
					ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, false, 30,
				)
				Expect(deploy).To(BeNil())
			}

			By("Deleting the AddOnDeploymentConfig")
			Kubectl("delete", "-f", addOnDeploymentConfigCR, "--timeout=15s")
			By("Restoring the governance-policy-framework ClusterManagementAddOn")
			Kubectl("apply", "-f", case1ClusterManagementAddOnCRDefault)
		})

	It("should use the onManagedClusterHub value set in helm values annotation", func(ctx SpecContext) {
		cluster := managedClusterList[0]
		Expect(cluster.clusterType).To(Equal("hub"))

		logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "

		By(logPrefix + "removing the on-multicluster-hub annotation on the ManagedCluster object")
		Kubectl(
			"annotate", "ManagedCluster", cluster.clusterName, "addon.open-cluster-management.io/on-multicluster-hub-",
		)

		DeferCleanup(
			Kubectl,
			"annotate",
			"ManagedCluster",
			cluster.clusterName,
			"--overwrite",
			"addon.open-cluster-management.io/on-multicluster-hub=true",
		)

		By(logPrefix + "deploying the annotated framework managedclusteraddon")
		Kubectl("apply", "-n", cluster.clusterName, "-f", case1hubValuesMCAOCR)
		deploy := GetWithTimeout(
			ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 60,
		)
		Expect(deploy).NotTo(BeNil())

		checkContainersAndAvailability(ctx, cluster, 0)

		checkArgs(ctx, cluster, "--disable-spec-sync=true")

		// Adding this annotation and later verifying the cluster namespace is not removed checks
		// that the helm values annotation and the logging level annotation are stackable.
		By(logPrefix + "annotating the managedclusteraddon with the " + loggingLevelAnnotation + " annotation")
		Kubectl("annotate", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, loggingLevelAnnotation)

		checkArgs(ctx, cluster, "--log-encoder=console", "--log-level=8", "--v=6")

		By(logPrefix + "deleting the managedclusteraddon")
		Kubectl("delete", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "--timeout=90s")
		deploy = GetWithTimeout(
			ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, false, 30,
		)
		Expect(deploy).To(BeNil())

		By(logPrefix + "checking the managed cluster namespace is not deleted after addon removed")
		Consistently(func() *unstructured.Unstructured {
			return GetWithTimeoutClusterResource(
				ctx, cluster.clusterClient, gvrNamespace, cluster.clusterName, true, 15)
		}, 30, 5).Should(Not(BeNil()))
	})

	It("should use the onMulticlusterHub value set in the custom annotation on the ManagedCluster object",
		func(ctx SpecContext) {
			cluster := managedClusterList[0]
			Expect(cluster.clusterType).To(Equal("hub"))

			logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "

			By(logPrefix + "relying on the annotated ManagedCluster object")
			Kubectl("apply", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR)
			deploy := GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 60,
			)
			Expect(deploy).NotTo(BeNil())

			checkContainersAndAvailability(ctx, cluster, 0)

			checkArgs(ctx, cluster, "--disable-spec-sync=true")

			By(logPrefix + "forcing the spec sync to be enabled on the hub")
			Kubectl(
				"annotate",
				"ManagedCluster",
				cluster.clusterName,
				"policy.open-cluster-management.io/sync-policies-on-multicluster-hub=true",
			)

			// This is a hack to trigger a reconcile.
			Kubectl(
				"-n",
				cluster.clusterName,
				"annotate",
				"ManagedClusterAddOn",
				case1ManagedClusterAddOnName,
				"trigger-reconcile="+time.Now().Format(time.RFC3339),
				"--overwrite",
			)

			By(logPrefix + "verifying that the spec sync is not disabled")

			Eventually(func(g Gomega) {
				deploy = GetWithTimeout(
					ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 60,
				)
				g.Expect(deploy).NotTo(BeNil())

				containers, _, _ := unstructured.NestedSlice(deploy.Object, "spec", "template", "spec", "containers")
				g.Expect(containers).To(HaveLen(1))

				args, ok := containers[0].(map[string]interface{})["args"].([]interface{})
				g.Expect(ok).To(BeTrue())

				for _, arg := range args {
					g.Expect(arg).ToNot(Equal("--disable-spec-sync=true"))
				}
			}, 60, 5).Should(Succeed())

			By(logPrefix + "cleaning up")

			Kubectl("annotate", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "policy-addon-pause=true")

			// This is hacky but this sets the ManifestWork to orphan everything so that we can remove the
			// policy.open-cluster-management.io/sync-policies-on-multicluster-hub annotation and not have it delete
			// the cluster namespace. This will get reset to SelectivelyOrphan when the addon controller is reconciled
			// below.
			Eventually(func(g Gomega) {
				mw := GetWithTimeout(ctx, clientDynamic, gvrManifestWork, case1MWName, cluster.clusterName, true, 15)

				err := unstructured.SetNestedField(mw.Object, "Orphan", "spec", "deleteOption", "propagationPolicy")
				g.Expect(err).ToNot(HaveOccurred())

				_, err = cluster.clusterClient.Resource(gvrManifestWork).Namespace(cluster.clusterName).Update(
					ctx, mw, metav1.UpdateOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())
			}, 30, 5).Should(Succeed())

			Kubectl(
				"annotate",
				"ManagedCluster",
				cluster.clusterName,
				"policy.open-cluster-management.io/sync-policies-on-multicluster-hub-",
			)

			Kubectl("annotate", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "policy-addon-pause-")

			// Wait for the ManifestWork to be updated to not reference the cluster namespace.
			Eventually(func(g Gomega) {
				mw := GetWithTimeout(ctx, clientDynamic, gvrManifestWork, case1MWName, cluster.clusterName, true, 15)
				manifests, _, _ := unstructured.NestedSlice(mw.Object, "spec", "workload", "manifests")

				for _, manifest := range manifests {
					if manifest.(map[string]interface{})["kind"] == "Namespace" {
						nsName := manifest.(map[string]interface{})["metadata"].(map[string]interface{})["name"]
						g.Expect(nsName).ToNot(Equal(cluster.clusterName))
					}
				}
			}, 30, 5).Should(Succeed())

			By(logPrefix + "deleting the managedclusteraddon")
			Kubectl("delete", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "--timeout=90s")
			deploy = GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, false, 30,
			)
			Expect(deploy).To(BeNil())
		})

	It("should use the onMulticlusterHub value set in the custom annotation", func(ctx SpecContext) {
		cluster := managedClusterList[0]
		Expect(cluster.clusterType).To(Equal("hub"))

		logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "

		By(logPrefix + "removing the on-multicluster-hub annotation on the ManagedCluster object")
		Kubectl(
			"annotate", "ManagedCluster", cluster.clusterName, "addon.open-cluster-management.io/on-multicluster-hub-",
		)

		DeferCleanup(
			Kubectl,
			"annotate",
			"ManagedCluster",
			cluster.clusterName,
			"--overwrite",
			"addon.open-cluster-management.io/on-multicluster-hub=true",
		)

		By(logPrefix + "deploying the annotated framework managedclusteraddon")
		Kubectl("apply", "-n", cluster.clusterName, "-f", case1hubAnnotationMCAOCR)
		deploy := GetWithTimeout(
			ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 60,
		)
		Expect(deploy).NotTo(BeNil())

		checkContainersAndAvailability(ctx, cluster, 0)

		checkArgs(ctx, cluster, "--disable-spec-sync=true")

		// Adding this annotation and later verifying the cluster namespace is not removed checks
		// that the multiclusterhub annotation and the logging level annotation are stackable.
		By(logPrefix + "annotating the managedclusteraddon with the " + loggingLevelAnnotation + " annotation")
		Kubectl("annotate", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, loggingLevelAnnotation)

		checkArgs(ctx, cluster, "--log-encoder=console", "--log-level=8", "--v=6")

		By(logPrefix + "deleting the managedclusteraddon")
		Kubectl("delete", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "--timeout=90s")
		deploy = GetWithTimeout(
			ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, false, 30,
		)
		Expect(deploy).To(BeNil())

		By(logPrefix + "checking the managed cluster namespace is not deleted after addon removed")
		Consistently(func() *unstructured.Unstructured {
			return GetWithTimeoutClusterResource(
				ctx, cluster.clusterClient, gvrNamespace, cluster.clusterName, true, 15)
		}, 30, 5).Should(Not(BeNil()))
	})

	It("should revert edits to the ManifestWork by default", func(ctx SpecContext) {
		for _, cluster := range managedClusterList {
			logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "
			By(logPrefix + "deploying the default framework managedclusteraddon")
			Kubectl("apply", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR)

			deploy := GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 60,
			)
			Expect(deploy).NotTo(BeNil())

			By(logPrefix + "getting the default number of items in the ManifestWork")
			defaultLength := 0
			Eventually(func() []interface{} {
				mw := GetWithTimeout(ctx, clientDynamic, gvrManifestWork, case1MWName, cluster.clusterName, true, 15)
				manifests, _, _ := unstructured.NestedSlice(mw.Object, "spec", "workload", "manifests")
				defaultLength = len(manifests)

				return manifests
			}, 60, 5).ShouldNot(BeEmpty())

			By(logPrefix + "patching the ManifestWork to add an item")
			Kubectl("patch", "-n", cluster.clusterName, "manifestwork", case1MWName, "--type=json",
				"--patch-file="+case1MWPatch)

			By(logPrefix + "verifying the edit is reverted")
			Eventually(func() []interface{} {
				mw := GetWithTimeout(ctx, clientDynamic, gvrManifestWork, case1MWName, cluster.clusterName, true, 15)
				manifests, _, _ := unstructured.NestedSlice(mw.Object, "spec", "workload", "manifests")

				return manifests
			}, 60, 5).Should(HaveLen(defaultLength))

			By(logPrefix + "deleting the managedclusteraddon")
			Kubectl("delete", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "--timeout=90s")
			deploy = GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, false, 30,
			)
			Expect(deploy).To(BeNil())
		}
	})

	It("should preserve edits to the ManifestWork if paused by annotation", func(ctx SpecContext) {
		for _, cluster := range managedClusterList {
			logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "
			By(logPrefix + "deploying the default framework managedclusteraddon")
			Kubectl("apply", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR)

			deploy := GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 60,
			)
			Expect(deploy).NotTo(BeNil())

			By(logPrefix + "annotating the managedclusteraddon with the pause annotation")
			Kubectl("annotate", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "policy-addon-pause=true")

			By(logPrefix + "getting the default number of items in the ManifestWork")
			defaultLength := 0
			Eventually(func() []interface{} {
				mw := GetWithTimeout(ctx, clientDynamic, gvrManifestWork, case1MWName, cluster.clusterName, true, 15)
				manifests, _, _ := unstructured.NestedSlice(mw.Object, "spec", "workload", "manifests")
				defaultLength = len(manifests)

				return manifests
			}, 60, 5).ShouldNot(BeEmpty())

			By(logPrefix + "patching the ManifestWork to add an item")
			Kubectl("patch", "-n", cluster.clusterName, "manifestwork", case1MWName, "--type=json",
				"--patch-file="+case1MWPatch)

			By(logPrefix + "verifying the edit is not reverted")
			Consistently(func() []interface{} {
				mw := GetWithTimeout(ctx, clientDynamic, gvrManifestWork, case1MWName, cluster.clusterName, true, 15)
				manifests, _, _ := unstructured.NestedSlice(mw.Object, "spec", "workload", "manifests")

				return manifests
			}, 30, 5).Should(HaveLen(defaultLength + 1))

			By(logPrefix + "removing the pause annotation")
			Kubectl("annotate", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "policy-addon-pause-")

			By(logPrefix + "verifying the edit is reverted after the annotation was removed")
			Eventually(func() []interface{} {
				mw := GetWithTimeout(ctx, clientDynamic, gvrManifestWork, case1MWName, cluster.clusterName, true, 15)
				manifests, _, _ := unstructured.NestedSlice(mw.Object, "spec", "workload", "manifests")

				return manifests
			}, 30, 5).Should(HaveLen(defaultLength))

			By(logPrefix + "deleting the managedclusteraddon")
			Kubectl("delete", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "--timeout=90s")
			deploy = GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, false, 30,
			)
			Expect(deploy).To(BeNil())
		}
	})

	It("should manage the cluster namespace on managed clusters", func(ctx SpecContext) {
		for _, cluster := range managedClusterList[1:] {
			Expect(cluster.clusterType).To(Equal("managed"))

			logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "
			By(logPrefix + "checking the managed cluster namespace does not exist before addon created")
			GetWithTimeoutClusterResource(ctx, cluster.clusterClient, gvrNamespace, cluster.clusterName, false, 15)

			By(logPrefix + "deploying the default framework managedclusteraddon")
			Kubectl("apply", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR)
			deploy := GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 60,
			)
			Expect(deploy).NotTo(BeNil())

			By(logPrefix + "checking the managed cluster namespace exists after addon created")
			GetWithTimeoutClusterResource(ctx, cluster.clusterClient, gvrNamespace, cluster.clusterName, true, 15)

			By(logPrefix + "deleting the managedclusteraddon")
			Kubectl("delete", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "--timeout=90s")
			deploy = GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, false, 30,
			)
			Expect(deploy).To(BeNil())

			By(logPrefix + "checking the managed cluster namespace is deleted after addon removed")
			GetWithTimeoutClusterResource(ctx, cluster.clusterClient, gvrNamespace, cluster.clusterName, false, 15)
		}
	})

	It("should deploy with startupProbes or initialDelaySeconds depending on version", func(ctx SpecContext) {
		for i, cluster := range managedClusterList[1:] {
			Expect(cluster.clusterType).To(Equal("managed"))

			logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "
			By(logPrefix + "deploying the default framework managedclusteraddon")
			Kubectl("apply", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR)
			deploy := GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, true, 60,
			)
			Expect(deploy).NotTo(BeNil())

			containers, found, err := unstructured.NestedSlice(deploy.Object, "spec", "template", "spec", "containers")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			container, ok := containers[0].(map[string]interface{})
			Expect(ok).To(BeTrue())

			// Use i+1 since the for loop ranges over a slice skipping first index
			if startupProbeInCluster(i + 1) {
				By(logPrefix + "checking for startupProbe on kubernetes 1.20 or higher")
				_, found, err = unstructured.NestedMap(container, "startupProbe")
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
			} else {
				By(logPrefix + "checking for initialDelaySeconds on kubernetes 1.19 or lower")
				_, found, err = unstructured.NestedInt64(container, "livenessProbe", "initialDelaySeconds")
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
			}

			By(logPrefix + "deleting the managedclusteraddon")
			Kubectl("delete", "-n", cluster.clusterName, "-f", case1ManagedClusterAddOnCR, "--timeout=90s")
			deploy = GetWithTimeout(
				ctx, cluster.clusterClient, gvrDeployment, case1DeploymentName, addonNamespace, false, 30,
			)
			Expect(deploy).To(BeNil())
		}
	})
})

func checkContainersAndAvailability(ctx context.Context, cluster managedClusterConfig, clusterIdx int) {
	namespace := addonNamespace

	if cluster.hostedOnHub {
		namespace = cluster.clusterName + "-hosted"
	}

	checkContainersAndAvailabilityInNamespace(ctx, cluster, clusterIdx, namespace)
}

func checkContainersAndAvailabilityInNamespace(
	ctx context.Context, cluster managedClusterConfig, clusterIdx int, installNamespace string,
) {
	logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "
	client := cluster.clusterClient

	if cluster.hostedOnHub {
		client = managedClusterList[0].clusterClient
	}

	namespace := installNamespace

	if startupProbeInCluster(clusterIdx) {
		By(logPrefix + "verifying all replicas in framework deployment are available")
		Eventually(func(g Gomega) {
			deploy := GetWithTimeout(
				ctx, client, gvrDeployment, case1DeploymentName, namespace, true, 60,
			)

			replicas, found, err := unstructured.NestedInt64(deploy.Object, "status", "replicas")
			g.Expect(found).To(BeTrue(), "status.replicas should exist in the deployment")
			g.Expect(err).ToNot(HaveOccurred())

			available, found, err := unstructured.NestedInt64(deploy.Object, "status", "availableReplicas")
			g.Expect(found).To(BeTrue(), "status.availableReplicas should exist in the deployment")
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(available).To(Equal(replicas), "available replicas should equal expected replicas")
		}, 240, 1).Should(Succeed())
	}

	By(logPrefix + "verifying one framework pod is running")
	Eventually(func() bool {
		opts := metav1.ListOptions{
			LabelSelector: case1PodSelector,
		}
		pods := ListWithTimeoutByNamespace(ctx, client, gvrPod, opts, namespace, 1, true, 30)

		phase, _, _ := unstructured.NestedString(pods.Items[0].Object, "status", "phase")

		return phase == "Running"
	}, 60, 1).Should(BeTrue())

	By(logPrefix + "showing the framework managedclusteraddon as available")
	Eventually(func() bool {
		addon := GetWithTimeout(
			ctx, clientDynamic, gvrManagedClusterAddOn, case1DeploymentName, cluster.clusterName, true, 30,
		)

		return getAddonStatus(addon)
	}, 240, 1).Should(BeTrue())
}

func checkArgs(ctx context.Context, cluster managedClusterConfig, desiredArgs ...string) {
	logPrefix := cluster.clusterType + " " + cluster.clusterName + ": "

	client := cluster.clusterClient
	namespace := addonNamespace

	if cluster.hostedOnHub {
		client = managedClusterList[0].clusterClient
		namespace = cluster.clusterName + "-hosted"
	}

	By(logPrefix + "verifying one framework pod is running and has the desired args")
	Eventually(func(g Gomega) {
		opts := metav1.ListOptions{
			LabelSelector: case1PodSelector,
		}
		pods := ListWithTimeoutByNamespace(ctx, client, gvrPod, opts, namespace, 1, true, 30)
		podObj := pods.Items[0].Object

		phase, found, err := unstructured.NestedString(podObj, "status", "phase")
		if err != nil || !found || phase != "Running" {
			g.Expect(err).ShouldNot(HaveOccurred(),
				fmt.Sprintf("pod phase is not running; found=%v; err=%v", found, err))
		}

		containerList, found, err := unstructured.NestedSlice(podObj, "spec", "containers")
		if err != nil || !found {
			g.Expect(err).ShouldNot(HaveOccurred(),
				fmt.Sprintf("could not get container list; found=%v; err=%v", found, err))
		}

		if len(containerList) != 1 {
			g.Expect(err).ShouldNot(HaveOccurred(),
				fmt.Sprintf("the container list had more than 1 entry; containerList=%v", containerList))
		}

		container := containerList[0]

		containerObj, ok := container.(map[string]interface{})
		if !ok {
			g.Expect(err).ShouldNot(HaveOccurred(),
				fmt.Sprintf("could not convert container to map; container=%v", container))
		}

		argList, found, err := unstructured.NestedStringSlice(containerObj, "args")
		if err != nil || !found {
			g.Expect(err).ShouldNot(HaveOccurred(),
				fmt.Sprintf("could not get container args; found=%v; err=%v", found, err))
		}

		g.Expect(argList).To(ContainElements(desiredArgs))
	}, 120, 1).Should(Succeed())
}

func startupProbeInCluster(clusterIdx int) bool {
	versionJSON := Kubectl(
		"version",
		"-o=json",
		fmt.Sprintf("--kubeconfig=%s%d_e2e", kubeconfigFilename, clusterIdx+1),
	)

	version := struct {
		ServerVersion struct {
			Minor int `json:"minor,string"`
		} `json:"serverVersion"`
	}{}

	if err := json.Unmarshal([]byte(versionJSON), &version); err != nil {
		// Deliberately panic and fail if the output didn't match what we expected
		p := fmt.Sprintf("error: %v, versionJSON: %v", err, versionJSON)
		panic(p)
	}

	return version.ServerVersion.Minor >= 20
}

func installAddonInHostedMode(
	ctx context.Context, logPrefix string, hubClient dynamic.Interface, addOnName, clusterName,
	hostingClusterName, installNamespace string, moreAnnotations map[string]string,
) {
	By(logPrefix + "deploying the " + addOnName + " ManagedClusterAddOn in hosted mode")

	addon := unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "addon.open-cluster-management.io/v1alpha1",
		"kind":       "ManagedClusterAddOn",
		"metadata": map[string]interface{}{
			"name": addOnName,
			"annotations": map[string]interface{}{
				"addon.open-cluster-management.io/hosting-cluster-name": hostingClusterName,
			},
		},
		"spec": map[string]interface{}{
			"installNamespace": installNamespace,
		},
	}}

	if moreAnnotations != nil {
		addonAnno := addon.GetAnnotations()
		for k, v := range moreAnnotations {
			addonAnno[k] = v
		}

		addon.SetAnnotations(addonAnno)
	}

	_, err := hubClient.Resource(gvrManagedClusterAddOn).Namespace(clusterName).Create(
		ctx, &addon, metav1.CreateOptions{},
	)
	Expect(err).ToNot(HaveOccurred())
}
