package main

import (
	"context"
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

func evictNodePods(nodeName string, client *kubernetes.Clientset) {

	pods, err := client.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, pod := range pods.Items {
		if pod.Namespace == "kube-system" {
			continue
		} else {
			log.Printf("Evicting %v from %v", pod.Name, nodeName)
			err := client.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			if err != nil {
				log.Fatal(err)
			}

		}
	}
}

func calculateScoresFromRenewables(nodeList *v1.NodeList) map[string]int {

	renewables := make(map[string]float64)

	// read renewable shares from node annotations
	for _, node := range nodeList.Items {

		log.Printf("Parsing renewable share of node %v", node.Name)
		renewableShare, err := strconv.ParseFloat(node.Annotations["renewable"], 64)
		if err != nil {
			log.Printf("Error parsing renewable share from node: %s \n", err.Error())
			renewableShare = 0
		}

		log.Printf("Node %v has a renewable share of %v", node.Name, renewableShare)
		renewables[node.Name] = float64(renewableShare)
	}

	return normalizeScores(renewables)
}

func normalizeScores(renewables map[string]float64) map[string]int {

	highest := 1.0
	scores := make(map[string]int)
	var score int

	for _, renewableShare := range renewables {
		highest = math.Max(highest, renewableShare)
		log.Printf("Highest share so far: %v", highest)
	}

	for node, renewableShare := range renewables {
		score = int(renewableShare * MAX_SCORE / highest)
		scores[node] = score
		log.Printf("Node %v has a score of %v", node, score)
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

		// list all nodes (only worker nodes?)
		nodeList, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			log.Printf("Error listing nodes: %v", err)
		}

		log.Printf("Following nodes are available in the cluster:")
		for _, node := range nodeList.Items {
			log.Printf(node.Name)
		}

		var scores map[string]int = calculateScoresFromRenewables(nodeList)
		var sortedScores PairList = sortScores(scores)

		switch MODE {
		case "evictFromWorst":
			log.Printf("Evicting Pods from Node with the lowest Score...")
			var lowestScoreNode string = sortedScores[0].Key
			var lowestScoreValue int = sortedScores[0].Value

			log.Printf("Node %v has the smallest renewable energy share with a score of %v... Evicting pods from node", lowestScoreNode, lowestScoreValue)
			evictNodePods(lowestScoreNode, clientset)

		case "onlyKeepBest":
			log.Printf("Evicting Pods from any Node except the one with the highest Score (%v)...", sortedScores[len(sortedScores)-1].Key)
			for _, pair := range sortedScores[:len(sortedScores)-1] {
				evictNodePods(pair.Key, clientset)
			}
		case "default":
			log.Printf("Default Case")
		}

		log.Printf("Sleeping...")
		time.Sleep(time.Duration(INTERVAL) * time.Second)
	}
}
