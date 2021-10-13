package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const MAX_SCORE = 10
const INTERVAL = 60
const MODE = "onlyKeepBest" // "evictFromWorst", "onlyKeepBest", "tbd"

type Pair struct {
	Key   string
	Value int
}

type PairList []Pair

func (p PairList) Len() int           { return len(p) }
func (p PairList) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p PairList) Less(i, j int) bool { return p[i].Value < p[j].Value }

func getNodePods(nodeName string, client *kubernetes.Clientset) map[string]string {

	pods_blitz := make(map[string]string)

	pods, err := client.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName + ",metadata.namespace=default",
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, pod := range pods.Items {
		pods_blitz[pod.Name] = string(pod.Status.Phase)
	}

	return pods_blitz
}

func evictNodePods(nodeName string, client *kubernetes.Clientset) {

	pods, err := client.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName + ",metadata.namespace=default",
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, pod := range pods.Items {
		log.Printf("Evicting %v from %v", pod.Name, nodeName)
		err := client.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
		if err != nil {
			log.Fatal(err)
		}
	}
}

func calculateScoresFromRenewables(nodeList *v1.NodeList) (map[string]int, map[string]float64) {

	renewables := make(map[string]float64)

	// read renewable shares from node annotations
	for _, node := range nodeList.Items {

		renewableShare, err := strconv.ParseFloat(node.Annotations["renewable"], 64)
		if err != nil {
			log.Printf("Error parsing renewable share from node: %s \n", err.Error())
			renewableShare = 0
		}

		renewables[node.Name] = float64(renewableShare)
	}

	return normalizeScores(renewables), renewables
}

func normalizeScores(renewables map[string]float64) map[string]int {

	highest := 1.0
	scores := make(map[string]int)
	var score int

	for _, renewableShare := range renewables {
		highest = math.Max(highest, renewableShare)
	}

	for node, renewableShare := range renewables {
		score = int(renewableShare * MAX_SCORE / highest)
		scores[node] = score
	}

	return scores
}

func sortScores(scores map[string]int) PairList {

	sortedScores := make(PairList, len(scores))

	i := 0
	for k, v := range scores {
		sortedScores[i] = Pair{k, v}
		i++
	}

	sort.Sort(sortedScores)

	return sortedScores
}

func main() {

	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Printf("Error creating in-cluster config: %v", err)
	}
	// creates the clientset
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
		var scores map[string]int
		var renewables map[string]float64

		scores, renewables = calculateScoresFromRenewables(nodeList)
		var sortedScores PairList = sortScores(scores)
		var maxIndex int = len(sortedScores) - 1

		switch MODE {
		case "evictFromWorst":
			if maxIndex == 0 {
				log.Printf("Only one Node available... Keeping Pods.")
			} else {
				evictNodePods(sortedScores[0].Key, clientset)
			}

		case "onlyKeepBest":
			if maxIndex == 0 {
				log.Printf("Only one Node available... Keeping Pods.")
			} else {
				for i := maxIndex; i > 0; i-- {
					// evicts the node at index i-1 if score is smaller
					if sortedScores.Less(i-1, maxIndex) {
						evictNodePods(sortedScores[i-1].Key, clientset)
					}
				}
			}

		default:
			log.Printf("Default Case")
		}
		// log some information for analysis TODO: Make this async to wait for new pods
		for node, score := range scores {
			log.Printf(node + ";" + fmt.Sprintf("%.2f", renewables[node]) + ";" + strconv.Itoa(score) + ";" + strconv.Itoa(len(getNodePods(node, clientset))))
		}
		time.Sleep(time.Duration(INTERVAL) * time.Second)
	}
}
