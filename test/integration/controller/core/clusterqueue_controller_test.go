/*
Copyright 2022 The Kubernetes Authors.

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

package core

import (
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"sigs.k8s.io/kueue/pkg/metrics"
	"sigs.k8s.io/kueue/pkg/util/testing"
	"sigs.k8s.io/kueue/pkg/workload"
	"sigs.k8s.io/kueue/test/util"
)

// +kubebuilder:docs-gen:collapse=Imports

const (
	resourceGPU corev1.ResourceName = "example.com/gpu"

	flavorOnDemand = "on-demand"
	flavorSpot     = "spot"
	flavorModelA   = "model-a"
	flavorModelB   = "model-b"
	flavorCPUArchA = "arch-a"
	flavorCPUArchB = "arch-b"
)

var ignoreConditionTimestamps = cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime")
var ignoreLastChangeTime = cmpopts.IgnoreFields(kueue.ClusterQueuePendingWorkloadsStatus{}, "LastChangeTime")
var ignorePendingWorkloadsStatus = cmpopts.IgnoreFields(kueue.ClusterQueueStatus{}, "PendingWorkloadsStatus")

var _ = ginkgo.Describe("ClusterQueue controller", func() {
	var (
		ns               *corev1.Namespace
		emptyUsedFlavors = []kueue.FlavorUsage{
			{
				Name: flavorOnDemand,
				Resources: []kueue.ResourceUsage{
					{Name: corev1.ResourceCPU},
				},
			},
			{
				Name: flavorSpot,
				Resources: []kueue.ResourceUsage{
					{Name: corev1.ResourceCPU},
				},
			},
			{
				Name: flavorModelA,
				Resources: []kueue.ResourceUsage{
					{Name: resourceGPU},
				},
			},
			{
				Name: flavorModelB,
				Resources: []kueue.ResourceUsage{
					{Name: resourceGPU},
				},
			},
		}
	)

	ginkgo.BeforeEach(func() {
		ns = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "core-clusterqueue-",
			},
		}
		gomega.Expect(k8sClient.Create(ctx, ns)).To(gomega.Succeed())
	})

	ginkgo.AfterEach(func() {
		gomega.Expect(util.DeleteNamespace(ctx, k8sClient, ns)).To(gomega.Succeed())
	})

	ginkgo.When("Reconciling clusterQueue usage status", func() {
		var (
			clusterQueue   *kueue.ClusterQueue
			localQueue     *kueue.LocalQueue
			onDemandFlavor *kueue.ResourceFlavor
			spotFlavor     *kueue.ResourceFlavor
			modelAFlavor   *kueue.ResourceFlavor
			modelBFlavor   *kueue.ResourceFlavor
		)

		ginkgo.BeforeEach(func() {
			clusterQueue = testing.MakeClusterQueue("cluster-queue").
				ResourceGroup(
					*testing.MakeFlavorQuotas(flavorOnDemand).
						Resource(corev1.ResourceCPU, "5", "5").Obj(),
					*testing.MakeFlavorQuotas(flavorSpot).
						Resource(corev1.ResourceCPU, "5", "5").Obj(),
				).
				ResourceGroup(
					*testing.MakeFlavorQuotas(flavorModelA).
						Resource(resourceGPU, "5", "5").Obj(),
					*testing.MakeFlavorQuotas(flavorModelB).
						Resource(resourceGPU, "5", "5").Obj(),
				).
				Cohort("cohort").
				Obj()
			gomega.Expect(k8sClient.Create(ctx, clusterQueue)).To(gomega.Succeed())
			localQueue = testing.MakeLocalQueue("queue", ns.Name).ClusterQueue(clusterQueue.Name).Obj()
			gomega.Expect(k8sClient.Create(ctx, localQueue)).To(gomega.Succeed())
		})

		ginkgo.AfterEach(func() {
			util.ExpectClusterQueueToBeDeleted(ctx, k8sClient, clusterQueue, true)
			util.ExpectResourceFlavorToBeDeleted(ctx, k8sClient, onDemandFlavor, true)
			util.ExpectResourceFlavorToBeDeleted(ctx, k8sClient, spotFlavor, true)
			util.ExpectResourceFlavorToBeDeleted(ctx, k8sClient, modelAFlavor, true)
			util.ExpectResourceFlavorToBeDeleted(ctx, k8sClient, modelBFlavor, true)
		})

		ginkgo.It("Should update status and report metrics when workloads are assigned and finish", func() {
			workloads := []*kueue.Workload{
				testing.MakeWorkload("one", ns.Name).Queue(localQueue.Name).
					Request(corev1.ResourceCPU, "2").Request(resourceGPU, "2").Obj(),
				testing.MakeWorkload("two", ns.Name).Queue(localQueue.Name).
					Request(corev1.ResourceCPU, "3").Request(resourceGPU, "3").Obj(),
				testing.MakeWorkload("three", ns.Name).Queue(localQueue.Name).
					Request(corev1.ResourceCPU, "1").Request(resourceGPU, "1").Obj(),
				testing.MakeWorkload("four", ns.Name).Queue(localQueue.Name).
					Request(corev1.ResourceCPU, "1").Request(resourceGPU, "1").Obj(),
				testing.MakeWorkload("five", ns.Name).Queue("other").
					Request(corev1.ResourceCPU, "1").Request(resourceGPU, "1").Obj(),
				testing.MakeWorkload("six", ns.Name).Queue(localQueue.Name).
					Request(corev1.ResourceCPU, "1").Request(resourceGPU, "1").Obj(),
			}

			ginkgo.By("Checking that the resource metrics are published", func() {
				util.ExpectCQResourceNominalQuota(clusterQueue, flavorOnDemand, string(corev1.ResourceCPU), 5)
				util.ExpectCQResourceNominalQuota(clusterQueue, flavorSpot, string(corev1.ResourceCPU), 5)
				util.ExpectCQResourceNominalQuota(clusterQueue, flavorModelA, string(resourceGPU), 5)
				util.ExpectCQResourceNominalQuota(clusterQueue, flavorModelB, string(resourceGPU), 5)

				util.ExpectCQResourceBorrowingQuota(clusterQueue, flavorOnDemand, string(corev1.ResourceCPU), 5)
				util.ExpectCQResourceBorrowingQuota(clusterQueue, flavorSpot, string(corev1.ResourceCPU), 5)
				util.ExpectCQResourceBorrowingQuota(clusterQueue, flavorModelA, string(resourceGPU), 5)
				util.ExpectCQResourceBorrowingQuota(clusterQueue, flavorModelB, string(resourceGPU), 5)

				util.ExpectCQResourceUsage(clusterQueue, flavorOnDemand, string(corev1.ResourceCPU), 0)
				util.ExpectCQResourceUsage(clusterQueue, flavorSpot, string(corev1.ResourceCPU), 0)
				util.ExpectCQResourceUsage(clusterQueue, flavorModelA, string(resourceGPU), 0)
				util.ExpectCQResourceUsage(clusterQueue, flavorModelB, string(resourceGPU), 0)
			})

			ginkgo.By("Creating workloads")
			for _, w := range workloads {
				gomega.Expect(k8sClient.Create(ctx, w)).To(gomega.Succeed())
			}
			gomega.Eventually(func() kueue.ClusterQueueStatus {
				var updatedCq kueue.ClusterQueue
				gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterQueue), &updatedCq)).To(gomega.Succeed())
				return updatedCq.Status
			}, util.Timeout, util.Interval).Should(gomega.BeComparableTo(kueue.ClusterQueueStatus{
				PendingWorkloads: 5,
				FlavorsUsage:     emptyUsedFlavors,
				Conditions: []metav1.Condition{
					{
						Type:    kueue.ClusterQueueActive,
						Status:  metav1.ConditionFalse,
						Reason:  "FlavorNotFound",
						Message: "Can't admit new workloads; some flavors are not found",
					},
				},
			}, ignoreConditionTimestamps, ignorePendingWorkloadsStatus))
			// Workloads are inadmissible because ResourceFlavors don't exist here yet.
			util.ExpectPendingWorkloadsMetric(clusterQueue, 0, 5)
			util.ExpectAdmittedActiveWorkloadsMetric(clusterQueue, 0)

			ginkgo.By("Creating ResourceFlavors")
			onDemandFlavor = testing.MakeResourceFlavor(flavorOnDemand).Obj()
			gomega.Expect(k8sClient.Create(ctx, onDemandFlavor)).To(gomega.Succeed())
			spotFlavor = testing.MakeResourceFlavor(flavorSpot).Obj()
			gomega.Expect(k8sClient.Create(ctx, spotFlavor)).To(gomega.Succeed())
			modelAFlavor = testing.MakeResourceFlavor(flavorModelA).Label(resourceGPU.String(), flavorModelA).Obj()
			gomega.Expect(k8sClient.Create(ctx, modelAFlavor)).To(gomega.Succeed())
			modelBFlavor = testing.MakeResourceFlavor(flavorModelB).Label(resourceGPU.String(), flavorModelB).Obj()
			gomega.Expect(k8sClient.Create(ctx, modelBFlavor)).To(gomega.Succeed())

			ginkgo.By("Admitting workloads")
			admissions := []*kueue.Admission{
				testing.MakeAdmission(clusterQueue.Name).
					Assignment(corev1.ResourceCPU, flavorOnDemand, "2").Assignment(resourceGPU, flavorModelA, "2").Obj(),
				testing.MakeAdmission(clusterQueue.Name).
					Assignment(corev1.ResourceCPU, flavorOnDemand, "3").Assignment(resourceGPU, flavorModelA, "3").Obj(),
				testing.MakeAdmission(clusterQueue.Name).
					Assignment(corev1.ResourceCPU, flavorOnDemand, "1").Assignment(resourceGPU, flavorModelB, "1").Obj(),
				testing.MakeAdmission(clusterQueue.Name).
					Assignment(corev1.ResourceCPU, flavorSpot, "1").Assignment(resourceGPU, flavorModelB, "1").Obj(),
				testing.MakeAdmission("other").
					Assignment(corev1.ResourceCPU, flavorSpot, "1").Assignment(resourceGPU, flavorModelB, "1").Obj(),
				nil,
			}
			for i, w := range workloads {
				gomega.Eventually(func() error {
					var newWL kueue.Workload
					gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(w), &newWL)).To(gomega.Succeed())
					if admissions[i] != nil {
						return util.SetQuotaReservation(ctx, k8sClient, &newWL, admissions[i])
					}
					return k8sClient.Status().Update(ctx, &newWL)
				}, util.Timeout, util.Interval).Should(gomega.Succeed())
			}

			gomega.Eventually(func() kueue.ClusterQueueStatus {
				var updatedCQ kueue.ClusterQueue
				gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterQueue), &updatedCQ)).To(gomega.Succeed())
				return updatedCQ.Status
			}, util.Timeout, util.Interval).Should(gomega.BeComparableTo(kueue.ClusterQueueStatus{
				PendingWorkloads:  1,
				AdmittedWorkloads: 4,
				FlavorsUsage: []kueue.FlavorUsage{
					{
						Name: flavorOnDemand,
						Resources: []kueue.ResourceUsage{{
							Name:     corev1.ResourceCPU,
							Total:    resource.MustParse("6"),
							Borrowed: resource.MustParse("1"),
						}},
					},
					{
						Name: flavorSpot,
						Resources: []kueue.ResourceUsage{{
							Name:  corev1.ResourceCPU,
							Total: resource.MustParse("1"),
						}},
					},
					{
						Name: flavorModelA,
						Resources: []kueue.ResourceUsage{{
							Name:  resourceGPU,
							Total: resource.MustParse("5"),
						}},
					},
					{
						Name: flavorModelB,
						Resources: []kueue.ResourceUsage{{
							Name:  resourceGPU,
							Total: resource.MustParse("2"),
						}},
					},
				},
				Conditions: []metav1.Condition{
					{
						Type:    kueue.ClusterQueueActive,
						Status:  metav1.ConditionTrue,
						Reason:  "Ready",
						Message: "Can admit new workloads",
					},
				},
			}, ignoreConditionTimestamps, ignorePendingWorkloadsStatus))
			util.ExpectPendingWorkloadsMetric(clusterQueue, 1, 0)
			util.ExpectAdmittedActiveWorkloadsMetric(clusterQueue, 4)

			ginkgo.By("Checking the resource usage metrics are updated", func() {
				util.ExpectCQResourceUsage(clusterQueue, flavorOnDemand, string(corev1.ResourceCPU), 6)
				util.ExpectCQResourceUsage(clusterQueue, flavorSpot, string(corev1.ResourceCPU), 1)
				util.ExpectCQResourceUsage(clusterQueue, flavorModelA, string(resourceGPU), 5)
				util.ExpectCQResourceUsage(clusterQueue, flavorModelB, string(resourceGPU), 2)
			})

			ginkgo.By("Finishing workloads")
			util.FinishWorkloads(ctx, k8sClient, workloads...)
			gomega.Eventually(func() kueue.ClusterQueueStatus {
				var updatedCq kueue.ClusterQueue
				gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterQueue), &updatedCq)).To(gomega.Succeed())
				return updatedCq.Status
			}, util.Timeout, util.Interval).Should(gomega.BeComparableTo(kueue.ClusterQueueStatus{
				FlavorsUsage: emptyUsedFlavors,
				Conditions: []metav1.Condition{
					{
						Type:    kueue.ClusterQueueActive,
						Status:  metav1.ConditionTrue,
						Reason:  "Ready",
						Message: "Can admit new workloads",
					},
				},
			}, ignoreConditionTimestamps, ignorePendingWorkloadsStatus))
			util.ExpectPendingWorkloadsMetric(clusterQueue, 0, 0)
			util.ExpectAdmittedActiveWorkloadsMetric(clusterQueue, 0)
		})

		ginkgo.It("Should update status when workloads have reclaimable pods", func() {

			ginkgo.By("Creating ResourceFlavors", func() {
				onDemandFlavor = testing.MakeResourceFlavor(flavorOnDemand).Obj()
				gomega.Expect(k8sClient.Create(ctx, onDemandFlavor)).To(gomega.Succeed())
				spotFlavor = testing.MakeResourceFlavor(flavorSpot).Obj()
				gomega.Expect(k8sClient.Create(ctx, spotFlavor)).To(gomega.Succeed())
				modelAFlavor = testing.MakeResourceFlavor(flavorModelA).Label(resourceGPU.String(), flavorModelA).Obj()
				gomega.Expect(k8sClient.Create(ctx, modelAFlavor)).To(gomega.Succeed())
				modelBFlavor = testing.MakeResourceFlavor(flavorModelB).Label(resourceGPU.String(), flavorModelB).Obj()
				gomega.Expect(k8sClient.Create(ctx, modelBFlavor)).To(gomega.Succeed())
			})

			wl := testing.MakeWorkload("one", ns.Name).
				Queue(localQueue.Name).
				PodSets(
					*testing.MakePodSet("driver", 2).
						Request(corev1.ResourceCPU, "1").
						Obj(),
					*testing.MakePodSet("workers", 5).
						Request(resourceGPU, "1").
						Obj(),
				).
				Obj()
			ginkgo.By("Creating the workload", func() {
				gomega.Expect(k8sClient.Create(ctx, wl)).To(gomega.Succeed())
				util.ExpectPendingWorkloadsMetric(clusterQueue, 1, 0)
			})

			ginkgo.By("Admitting the workload", func() {
				admission := testing.MakeAdmission(clusterQueue.Name).PodSets(
					kueue.PodSetAssignment{
						Name: "driver",
						Flavors: map[corev1.ResourceName]kueue.ResourceFlavorReference{
							corev1.ResourceCPU: "on-demand",
						},
						ResourceUsage: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
						Count: ptr.To[int32](2),
					},
					kueue.PodSetAssignment{
						Name: "workers",
						Flavors: map[corev1.ResourceName]kueue.ResourceFlavorReference{
							resourceGPU: "model-a",
						},
						ResourceUsage: corev1.ResourceList{
							resourceGPU: resource.MustParse("5"),
						},
						Count: ptr.To[int32](5),
					},
				).Obj()

				gomega.Eventually(func() error {
					var newWL kueue.Workload
					gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wl), &newWL)).To(gomega.Succeed())
					return util.SetQuotaReservation(ctx, k8sClient, &newWL, admission)
				}, util.Timeout, util.Interval).Should(gomega.Succeed())
			})

			util.ExpectAdmittedActiveWorkloadsMetric(clusterQueue, 1)
			gomega.Eventually(func() []kueue.FlavorUsage {
				var updatedCq kueue.ClusterQueue
				gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterQueue), &updatedCq)).To(gomega.Succeed())
				return updatedCq.Status.FlavorsUsage
			}, util.Timeout, util.Interval).Should(gomega.BeComparableTo([]kueue.FlavorUsage{
				{
					Name: flavorOnDemand,
					Resources: []kueue.ResourceUsage{{
						Name:  corev1.ResourceCPU,
						Total: resource.MustParse("2"),
					}},
				},
				{
					Name: flavorSpot,
					Resources: []kueue.ResourceUsage{{
						Name: corev1.ResourceCPU,
					}},
				},
				{
					Name: flavorModelA,
					Resources: []kueue.ResourceUsage{{
						Name:  resourceGPU,
						Total: resource.MustParse("5"),
					}},
				},
				{
					Name: flavorModelB,
					Resources: []kueue.ResourceUsage{{
						Name: resourceGPU,
					}},
				},
			}, ignoreConditionTimestamps))

			ginkgo.By("Mark two workers as reclaimable", func() {
				gomega.Expect(workload.UpdateReclaimablePods(ctx, k8sClient, wl, []kueue.ReclaimablePod{{Name: "workers", Count: 2}})).To(gomega.Succeed())

				util.ExpectAdmittedActiveWorkloadsMetric(clusterQueue, 1)
				gomega.Eventually(func() []kueue.FlavorUsage {
					var updatedCq kueue.ClusterQueue
					gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterQueue), &updatedCq)).To(gomega.Succeed())
					return updatedCq.Status.FlavorsUsage
				}, util.Timeout, util.Interval).Should(gomega.BeComparableTo([]kueue.FlavorUsage{
					{
						Name: flavorOnDemand,
						Resources: []kueue.ResourceUsage{{
							Name:  corev1.ResourceCPU,
							Total: resource.MustParse("2"),
						}},
					},
					{
						Name: flavorSpot,
						Resources: []kueue.ResourceUsage{{
							Name: corev1.ResourceCPU,
						}},
					},
					{
						Name: flavorModelA,
						Resources: []kueue.ResourceUsage{{
							Name:  resourceGPU,
							Total: resource.MustParse("3"),
						}},
					},
					{
						Name: flavorModelB,
						Resources: []kueue.ResourceUsage{{
							Name: resourceGPU,
						}},
					},
				}, ignoreConditionTimestamps))
			})

			ginkgo.By("Mark all workers and a driver as reclaimable", func() {
				gomega.Expect(workload.UpdateReclaimablePods(ctx, k8sClient, wl, []kueue.ReclaimablePod{{Name: "workers", Count: 5}, {Name: "driver", Count: 1}})).To(gomega.Succeed())

				util.ExpectAdmittedActiveWorkloadsMetric(clusterQueue, 1)
				gomega.Eventually(func() []kueue.FlavorUsage {
					var updatedCq kueue.ClusterQueue
					gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterQueue), &updatedCq)).To(gomega.Succeed())
					return updatedCq.Status.FlavorsUsage
				}, util.Timeout, util.Interval).Should(gomega.BeComparableTo([]kueue.FlavorUsage{
					{
						Name: flavorOnDemand,
						Resources: []kueue.ResourceUsage{{
							Name:  corev1.ResourceCPU,
							Total: resource.MustParse("1"),
						}},
					},
					{
						Name: flavorSpot,
						Resources: []kueue.ResourceUsage{{
							Name: corev1.ResourceCPU,
						}},
					},
					{
						Name: flavorModelA,
						Resources: []kueue.ResourceUsage{{
							Name: resourceGPU,
						}},
					},
					{
						Name: flavorModelB,
						Resources: []kueue.ResourceUsage{{
							Name: resourceGPU,
						}},
					},
				}, ignoreConditionTimestamps))
			})

			ginkgo.By("Finishing workload", func() {
				util.FinishWorkloads(ctx, k8sClient, wl)
				util.ExpectPendingWorkloadsMetric(clusterQueue, 0, 0)
				util.ExpectAdmittedActiveWorkloadsMetric(clusterQueue, 0)
			})

		})
	})

	ginkgo.When("Reconciling clusterQueue status condition", func() {
		var (
			cq             *kueue.ClusterQueue
			lq             *kueue.LocalQueue
			wl             *kueue.Workload
			cpuArchAFlavor *kueue.ResourceFlavor
			cpuArchBFlavor *kueue.ResourceFlavor
		)

		ginkgo.BeforeEach(func() {
			cq = testing.MakeClusterQueue("bar-cq").
				ResourceGroup(
					*testing.MakeFlavorQuotas(flavorCPUArchA).Resource(corev1.ResourceCPU, "5", "5").Obj(),
					*testing.MakeFlavorQuotas(flavorCPUArchB).Resource(corev1.ResourceCPU, "5", "5").Obj(),
				).Obj()
			gomega.Expect(k8sClient.Create(ctx, cq)).To(gomega.Succeed())
			lq = testing.MakeLocalQueue("bar-lq", ns.Name).ClusterQueue(cq.Name).Obj()
			gomega.Expect(k8sClient.Create(ctx, lq)).To(gomega.Succeed())
			wl = testing.MakeWorkload("bar-wl", ns.Name).Queue(lq.Name).Obj()
			gomega.Expect(k8sClient.Create(ctx, wl)).To(gomega.Succeed())
		})

		ginkgo.AfterEach(func() {
			gomega.Expect(util.DeleteWorkload(ctx, k8sClient, wl)).To(gomega.Succeed())
			gomega.Expect(util.DeleteLocalQueue(ctx, k8sClient, lq)).To(gomega.Succeed())
			util.ExpectClusterQueueToBeDeleted(ctx, k8sClient, cq, true)
			util.ExpectResourceFlavorToBeDeleted(ctx, k8sClient, cpuArchAFlavor, true)
			util.ExpectResourceFlavorToBeDeleted(ctx, k8sClient, cpuArchBFlavor, true)
		})

		ginkgo.It("Should update status conditions when flavors are created", func() {
			ginkgo.By("All Flavors are not found")

			gomega.Eventually(func() []metav1.Condition {
				var updatedCq kueue.ClusterQueue
				gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cq), &updatedCq)).To(gomega.Succeed())
				return updatedCq.Status.Conditions
			}, util.Timeout, util.Interval).Should(gomega.BeComparableTo([]metav1.Condition{
				{
					Type:    kueue.ClusterQueueActive,
					Status:  metav1.ConditionFalse,
					Reason:  "FlavorNotFound",
					Message: "Can't admit new workloads; some flavors are not found",
				},
			}, ignoreConditionTimestamps))

			ginkgo.By("One of flavors is not found")
			cpuArchAFlavor = testing.MakeResourceFlavor(flavorCPUArchA).Obj()
			gomega.Expect(k8sClient.Create(ctx, cpuArchAFlavor)).To(gomega.Succeed())
			gomega.Eventually(func() []metav1.Condition {
				var updatedCq kueue.ClusterQueue
				gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cq), &updatedCq)).To(gomega.Succeed())
				return updatedCq.Status.Conditions
			}, util.Timeout, util.Interval).Should(gomega.BeComparableTo([]metav1.Condition{
				{
					Type:    kueue.ClusterQueueActive,
					Status:  metav1.ConditionFalse,
					Reason:  "FlavorNotFound",
					Message: "Can't admit new workloads; some flavors are not found",
				},
			}, ignoreConditionTimestamps))

			ginkgo.By("All flavors are created")
			cpuArchBFlavor = testing.MakeResourceFlavor(flavorCPUArchB).Obj()
			gomega.Expect(k8sClient.Create(ctx, cpuArchBFlavor)).To(gomega.Succeed())
			gomega.Eventually(func() []metav1.Condition {
				var updatedCq kueue.ClusterQueue
				gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cq), &updatedCq)).To(gomega.Succeed())
				return updatedCq.Status.Conditions
			}, util.Timeout, util.Interval).Should(gomega.BeComparableTo([]metav1.Condition{
				{
					Type:    kueue.ClusterQueueActive,
					Status:  metav1.ConditionTrue,
					Reason:  "Ready",
					Message: "Can admit new workloads",
				},
			}, ignoreConditionTimestamps))
		})
	})

	ginkgo.When("Deleting clusterQueues", func() {
		var (
			cq *kueue.ClusterQueue
			lq *kueue.LocalQueue
		)

		ginkgo.BeforeEach(func() {
			cq = testing.MakeClusterQueue("foo-cq").Obj()
			lq = testing.MakeLocalQueue("queue", ns.Name).ClusterQueue(cq.Name).Obj()
			gomega.Expect(k8sClient.Create(ctx, lq)).To(gomega.Succeed())
			gomega.Expect(k8sClient.Create(ctx, cq)).To(gomega.Succeed())
		})

		ginkgo.It("Should delete clusterQueues successfully when no admitted workloads are running", func() {
			util.ExpectClusterQueueToBeDeleted(ctx, k8sClient, cq, true)
		})

		ginkgo.It("Should be stuck in termination until admitted workloads finished running", func() {
			util.ExpectClusterQueueStatusMetric(cq, metrics.CQStatusActive)

			ginkgo.By("Admit workload")
			wl := testing.MakeWorkload("workload", ns.Name).Queue(lq.Name).Obj()
			gomega.Expect(k8sClient.Create(ctx, wl)).To(gomega.Succeed())
			gomega.Expect(util.SetQuotaReservation(ctx, k8sClient, wl, testing.MakeAdmission(cq.Name).Obj())).To(gomega.Succeed())

			ginkgo.By("Delete clusterQueue")
			gomega.Expect(util.DeleteClusterQueue(ctx, k8sClient, cq)).To(gomega.Succeed())
			util.ExpectClusterQueueStatusMetric(cq, metrics.CQStatusTerminating)
			var newCQ kueue.ClusterQueue
			gomega.Eventually(func() []string {
				gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cq), &newCQ)).To(gomega.Succeed())
				return newCQ.GetFinalizers()
			}, util.Timeout, util.Interval).Should(gomega.Equal([]string{kueue.ResourceInUseFinalizerName}))

			ginkgo.By("Finish workload")
			util.FinishWorkloads(ctx, k8sClient, wl)

			ginkgo.By("The clusterQueue will be deleted")
			gomega.Eventually(func() error {
				var newCQ kueue.ClusterQueue
				return k8sClient.Get(ctx, client.ObjectKeyFromObject(cq), &newCQ)
			}, util.Timeout, util.Interval).Should(testing.BeNotFoundError())
		})
	})

	ginkgo.When("Reconciling clusterQueue pending workload status", func() {
		var (
			clusterQueue   *kueue.ClusterQueue
			localQueue     *kueue.LocalQueue
			onDemandFlavor *kueue.ResourceFlavor
		)

		ginkgo.BeforeEach(func() {
			onDemandFlavor = testing.MakeResourceFlavor(flavorOnDemand).Obj()
			gomega.Expect(k8sClient.Create(ctx, onDemandFlavor)).To(gomega.Succeed())
			clusterQueue = testing.MakeClusterQueue("cluster-queue").
				ResourceGroup(
					*testing.MakeFlavorQuotas(flavorOnDemand).
						Resource(corev1.ResourceCPU, "5", "5").Obj(),
				).
				Obj()
			gomega.Expect(k8sClient.Create(ctx, clusterQueue)).To(gomega.Succeed())
			localQueue = testing.MakeLocalQueue("queue", ns.Name).ClusterQueue(clusterQueue.Name).Obj()
			gomega.Expect(k8sClient.Create(ctx, localQueue)).To(gomega.Succeed())
		})

		ginkgo.AfterEach(func() {
			util.ExpectClusterQueueToBeDeleted(ctx, k8sClient, clusterQueue, true)
			util.ExpectResourceFlavorToBeDeleted(ctx, k8sClient, onDemandFlavor, true)
		})

		ginkgo.It("Should update of the pending workloads when a new workload is scheduled", func() {
			const lowPrio, midPrio, highPrio = 0, 10, 100
			workloads := []*kueue.Workload{
				testing.MakeWorkload("one", ns.Name).Queue(localQueue.Name).Priority(highPrio).
					Request(corev1.ResourceCPU, "2").Request(resourceGPU, "2").Obj(),
				testing.MakeWorkload("two", ns.Name).Queue(localQueue.Name).Priority(midPrio).
					Request(corev1.ResourceCPU, "3").Request(resourceGPU, "3").Obj(),
			}

			gomega.Eventually(func() *kueue.ClusterQueuePendingWorkloadsStatus {
				var updatedCq kueue.ClusterQueue
				gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterQueue), &updatedCq)).To(gomega.Succeed())
				return updatedCq.Status.PendingWorkloadsStatus
			}, util.Timeout, util.Interval).Should(gomega.BeComparableTo(&kueue.ClusterQueuePendingWorkloadsStatus{}, ignoreLastChangeTime))

			ginkgo.By("Creating workloads")
			for _, w := range workloads {
				gomega.Expect(k8sClient.Create(ctx, w)).To(gomega.Succeed())
			}
			gomega.Eventually(func() *kueue.ClusterQueuePendingWorkloadsStatus {
				var updatedCq kueue.ClusterQueue
				gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterQueue), &updatedCq)).To(gomega.Succeed())
				return updatedCq.Status.PendingWorkloadsStatus
			}, util.Timeout, util.Interval).Should(gomega.BeComparableTo(&kueue.ClusterQueuePendingWorkloadsStatus{
				Head: []kueue.ClusterQueuePendingWorkload{
					{
						Name:      "one",
						Namespace: ns.Name,
					},
					{
						Name:      "two",
						Namespace: ns.Name,
					},
				},
			}, ignoreLastChangeTime))

			ginkgo.By("Creating a new workload")
			newWorkloads := []*kueue.Workload{
				testing.MakeWorkload("three", ns.Name).Queue(localQueue.Name).Priority(midPrio).
					Request(corev1.ResourceCPU, "2").Request(resourceGPU, "2").Obj(),
				testing.MakeWorkload("four", ns.Name).Queue(localQueue.Name).Priority(lowPrio).
					Request(corev1.ResourceCPU, "3").Request(resourceGPU, "3").Obj(),
			}
			for _, w := range newWorkloads {
				gomega.Expect(k8sClient.Create(ctx, w)).To(gomega.Succeed())
			}

			ginkgo.By("Cut off the head of pending workloads when the number of pending workloads exceeds MaxCount")
			gomega.Eventually(func() *kueue.ClusterQueuePendingWorkloadsStatus {
				var updatedCq kueue.ClusterQueue
				gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterQueue), &updatedCq)).To(gomega.Succeed())
				return updatedCq.Status.PendingWorkloadsStatus
			}, util.Timeout, util.Interval).Should(gomega.BeComparableTo(&kueue.ClusterQueuePendingWorkloadsStatus{
				Head: []kueue.ClusterQueuePendingWorkload{
					{
						Name:      "one",
						Namespace: ns.Name,
					},
					{
						Name:      "two",
						Namespace: ns.Name,
					},
					{
						Name:      "three",
						Namespace: ns.Name,
					},
				},
			}, ignoreLastChangeTime))

			ginkgo.By("Admitting workloads")
			for _, w := range workloads {
				gomega.Eventually(func() error {
					var newWL kueue.Workload
					gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(w), &newWL)).To(gomega.Succeed())
					return util.SetQuotaReservation(ctx, k8sClient, &newWL, testing.MakeAdmission(clusterQueue.Name).Obj())
				}, util.Timeout, util.Interval).Should(gomega.Succeed())
			}

			gomega.Eventually(func() *kueue.ClusterQueuePendingWorkloadsStatus {
				var updatedCQ kueue.ClusterQueue
				gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterQueue), &updatedCQ)).To(gomega.Succeed())
				return updatedCQ.Status.PendingWorkloadsStatus
			}, util.Timeout, util.Interval).Should(gomega.BeComparableTo(&kueue.ClusterQueuePendingWorkloadsStatus{
				Head: []kueue.ClusterQueuePendingWorkload{
					{
						Name:      "three",
						Namespace: ns.Name,
					},
					{
						Name:      "four",
						Namespace: ns.Name,
					},
				},
			}, ignoreLastChangeTime))

			ginkgo.By("Finishing workload", func() {
				util.FinishWorkloads(ctx, k8sClient, workloads...)
				util.FinishWorkloads(ctx, k8sClient, newWorkloads...)
			})

			gomega.Eventually(func() *kueue.ClusterQueuePendingWorkloadsStatus {
				var updatedCq kueue.ClusterQueue
				gomega.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterQueue), &updatedCq)).To(gomega.Succeed())
				return updatedCq.Status.PendingWorkloadsStatus
			}, util.Timeout, util.Interval).Should(gomega.BeComparableTo(&kueue.ClusterQueuePendingWorkloadsStatus{
				Head: []kueue.ClusterQueuePendingWorkload{},
			}, ignoreLastChangeTime))
		})
	})
})
