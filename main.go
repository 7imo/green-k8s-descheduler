package main

import (
	"context"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const MAX_SCORE = 100
const INTERVAL = 60

type Pair struct {
	Key   string
	Value int
}

type PairList []Pair

func (p PairList) Len() int           { return len(p) }
func (p PairList) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p PairList) Less(i, j int) bool { return p[i].Value < p[j].Value }

func getInterval() int {
	// get descheduling interval / other variables (Threshold?) from config
	var env_interval string = os.Getenv("DESCHEDULING_INTERVAL")

	interval, err := strconv.Atoi(env_interval)
	if err == nil {
		log.Printf("The current descheduling interval is %v", interval)
	}

	return interval
}

func evictNodePods(nodeName string, client *kubernetes.Clientset) {

	pods, err := client.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Following pods are going to be evicted from node %v: %v", nodeName, pods.Items)

	for _, i := range pods.Items {
		if i.Namespace == "kube-system" {
			continue
		} else {
			err := client.CoreV1().Pods(i.Namespace).Delete(context.TODO(), i.Name, metav1.DeleteOptions{})
			if err != nil {
				log.Fatal(err)
			}
		}
	}
}

func CalculateScoresFromRenewables(nodeList *v1.NodeList) map[string]int {

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

	return NormalizeScores(renewables)
}

func NormalizeScores(renewables map[string]float64) map[string]int {

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

func SortScores(scores map[string]int) PairList {

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

		//var interval int = getInterval()

		// list all nodes (only worker nodes?)
		nodeList, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			log.Printf("Error listing nodes: %v", err)
		}

		log.Printf("Following nodes are available in the cluster: %v", nodeList.Items)

		var scores map[string]int = CalculateScoresFromRenewables(nodeList)

		var sortedScores PairList = SortScores(scores)

		var lowestScoreNode string = sortedScores[len(sortedScores)-1].Key

		log.Printf("Node %v has the smallest renewable energy share / score... Evicting pods from node", lowestScoreNode)
		evictNodePods(lowestScoreNode, clientset)

		time.Sleep(time.Duration(INTERVAL) * time.Second)
	}
}
