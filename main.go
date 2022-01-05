package main

import (
	"context"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metrics "k8s.io/metrics/pkg/client/clientset/versioned"
)

// read config from descheduler.yaml
var INTERVAL = os.Getenv("INTERVAL")
var THRESHOLD = os.Getenv("THRESHOLD")

// node power constant
const RATED_POWER_NODE = 10000

func parseDataFromNodes(nodeList *v1.NodeList, windowSize int) map[string][]float64 {

	nodeEnergyData := make(map[string][]float64)

	// read renewable shares from node annotations
	for _, node := range nodeList.Items {

		var energyData []float64

		sharesString := node.Annotations["renewables"]
		if sharesString == "" {
			log.Printf("Error parsing renewable share from node %v: No values found. Assigning renewable energy shares of 0.", node.Name)
			for i := 0; i < windowSize; i += 1 {
				energyData = append(energyData, 0.0)
			}
		} else {
			// split renewable string into slice with single values
			shares := strings.Split(sharesString, ";")
			// convert strings to floats and append to data
			for i := 0; i < windowSize; i += 1 {
				f64, _ := strconv.ParseFloat(shares[i], 64)
				energyData = append(energyData, float64(f64))
			}
		}

		// Logs for Debugging
		log.Printf("Renewable shares parsed from Node %v: %v", node.Name, energyData)

		nodeEnergyData[node.Name] = energyData
	}

	return nodeEnergyData
}

func calculateCpuUtilization(nodeList *v1.NodeList) map[string]float64 {

	nodeUtilization := make(map[string]float64)
	var kubeconfig, master string //empty, assuming inClusterConfig

	// initiate connection to metrics server
	config, err := clientcmd.BuildConfigFromFlags(master, kubeconfig)
	if err != nil {
		panic(err)
	}

	mc, err := metrics.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	for _, node := range nodeList.Items {

		// get node metrics from metrics server
		nodeMetricsList, err := mc.MetricsV1beta1().NodeMetricses().Get(context.TODO(), node.Name, metav1.GetOptions{})
		if err != nil {
			panic(err)
		}

		// get total allocatable CPU from node status
		cpuAllocatableCores, _ := strconv.ParseFloat(node.Status.Allocatable.Cpu().String(), 64)
		var cpuAllocatableNanoCores = cpuAllocatableCores * math.Pow(10, 9)

		// get current CPU utilization from node metrics
		cpuCurrentUsageNanoCores, _ := strconv.ParseFloat(strings.TrimSuffix(nodeMetricsList.Usage.Cpu().String(), "n"), 64)

		var totalUtilization = cpuCurrentUsageNanoCores / cpuAllocatableNanoCores

		nodeUtilization[node.Name] = totalUtilization
	}

	return nodeUtilization
}

func calculateRenewableExcess(energyData map[string][]float64, currentUtilization map[string]float64) map[string][]float64 {

	renewablesExcess := make(map[string][]float64)

	for node := range energyData {

		var nodeRenewableExcess []float64
		var currentNodeUtilization = currentUtilization[node]

		// calculate consumption and round to two decimal places
		var currentConsumption = roundToTwoDecimals(RATED_POWER_NODE * currentNodeUtilization)

		log.Printf("Node %v with max input of %v W and current utilization of %v %% has a current consumption of %v W", node, RATED_POWER_NODE, math.Round(currentNodeUtilization*1000)/10, currentConsumption)

		// calculate renewable energy excess for current node utilization
		for _, renewableShare := range energyData[node] {
			var excess = roundToTwoDecimals(renewableShare - currentConsumption)
			nodeRenewableExcess = append(nodeRenewableExcess, excess)
		}

		log.Printf("Node %v has a renewable energy excess share of: %v ", node, nodeRenewableExcess)

		renewablesExcess[node] = nodeRenewableExcess
	}

	return renewablesExcess
}

func roundToTwoDecimals(input float64) float64 {
	return math.Round(input*100) / 100
}

func getNodePodCount(nodeName string, clientset *kubernetes.Clientset) int {

	pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName + ",metadata.namespace=default",
	})
	if err != nil {
		log.Fatal(err)
	}

	return len(pods.Items)
}

func evictNodePods(nodeName string, podCount int, clientset *kubernetes.Clientset) {
	// deletes all pods in default namespace from node
	pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName + ",metadata.namespace=default",
	})
	if err != nil {
		log.Fatal(err)
	}

	for i, pod := range pods.Items {
		log.Printf("Evicting %v from %v", pod.Name, nodeName)
		err := clientset.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
		if err != nil {
			log.Fatal(err)
		}

		if i+1 == podCount {
			break
		}
	}
}

func checkIfDeschedulingPossible(renewableExcess map[string][]float64, clientset *kubernetes.Clientset) bool {

	var positive = 0
	var negative = 0
	var totalPods = 0

	for node := range renewableExcess {
		totalPods += getNodePodCount(node, clientset)
	}
	// no descheduling required if no Pods in cluster
	if totalPods == 0 {
		log.Printf("No descheduling required: No Pods in cluster")
		return false
	}

	for _, excess := range renewableExcess {
		for i := range excess {
			if excess[i] > 0 {
				positive++
			} else if excess[i] <= 0 {
				negative++
			}
		}
	}

	// no descheduling required if all nodes have negative excess / all nodes have positive excess
	if negative == 0 || positive == 0 {
		return false
		log.Printf("No descheduling required: pos: %v, neg: %v", positive, negative)
	} else {
		log.Printf("Descheduling required: pos: %v, neg: %v", positive, negative)
		return true
	}
}

func assessCandidates(renewableExcess map[string][]float64, currentUtilization map[string]float64, nodePodCount map[string]int, thresholdFactor float64) (string, int) {

	var lowestExcess = 0.0
	var highestExcess = 0.0
	var deschedulingCandidate string
	var consumptionPerPod float64

	for node, podCount := range nodePodCount {

		if podCount > 0 {
			if renewableExcess[node][0] < lowestExcess {
				lowestExcess = renewableExcess[node][0]
				deschedulingCandidate = node
			}
		}

		if renewableExcess[node][0] > highestExcess {
			highestExcess = renewableExcess[node][0]
		}
	}

	consumptionPerPod = roundToTwoDecimals(RATED_POWER_NODE * currentUtilization[deschedulingCandidate] / float64(nodePodCount[deschedulingCandidate]))

	for i := nodePodCount[deschedulingCandidate]; i > 0; i-- {
		if highestExcess-consumptionPerPod*float64(i) > lowestExcess*thresholdFactor {
			log.Printf("Eviction Recommendation: %v Pod(s) from Node %v", i, deschedulingCandidate)
			return deschedulingCandidate, i
		} else {
			log.Printf("Not enough renewables to deschedule %v Pod(s) from Node %v: %v - %v * %v not greater than %v", i, deschedulingCandidate, highestExcess, consumptionPerPod, i, lowestExcess*thresholdFactor)
		}
	}

	return deschedulingCandidate, 0
}

func countPodsOnNodes(nodeList *v1.NodeList, clientset *kubernetes.Clientset) map[string]int {

	nodePodCount := make(map[string]int)

	for _, node := range nodeList.Items {
		nodePodCount[node.Name] = getNodePodCount(node.Name, clientset)
	}

	return nodePodCount
}

func main() {
	// considers current renewable energy availability
	var windowSize = 1

	// get threshold from config
	THRESHOLD, err := strconv.Atoi(THRESHOLD)
	if err != nil {
		log.Printf("Error converting Threshold String: %v - Setting default Threshold of 0", err)
		THRESHOLD = 0
	}

	var thresholdFactor = (100 - float64(THRESHOLD)) / 100

	// get interval from config
	INTERVAL, err := strconv.Atoi(INTERVAL)
	if err != nil {
		log.Printf("Error converting Interval String: %v - Setting default Interval of 10 Minutes", err)
		INTERVAL = 600
	}

	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Printf("Error creating in-cluster config: %v", err)
	}
	// creates the global clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Printf("Error creating clientset: %v", err)
	}

	for {

		// list all worker nodes
		nodeList, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "kubernetes.io/role=node"})
		if err != nil {
			log.Printf("Error listing nodes: %v", err)
		}

		var energyData = parseDataFromNodes(nodeList, windowSize)
		var currentUtilization = calculateCpuUtilization(nodeList)
		var renewableExcess = calculateRenewableExcess(energyData, currentUtilization)
		var nodePodCount = countPodsOnNodes(nodeList, clientset)
		// checks if there is an imbalance in renewable excess energy
		var deschedulingPossible = checkIfDeschedulingPossible(renewableExcess, clientset)

		// descheduling
		if deschedulingPossible {
			// determines node to deschedule from and number of pods that can be descheduled
			var deschedulingCandidate, podCount = assessCandidates(renewableExcess, currentUtilization, nodePodCount, thresholdFactor)
			if podCount > 0 {
				evictNodePods(deschedulingCandidate, podCount, clientset)
			}
		}

		// descheduling interval
		time.Sleep(time.Duration(INTERVAL) * time.Second)
	}
}
