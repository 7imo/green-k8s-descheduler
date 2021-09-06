package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {

	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	for {

		var interval int = getConfig()

		// list all nodes (only worker nodes?)
		nodeList, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			log.Printf("Error listing nodes: %v", err)
		}

		//https://github.com/kubernetes/kubectl/issues/1021

		nodeScoreMap := make(map[string]int)
		nodes := nodeList.Items
		var score int

		// read renewable shares from node annotations
		for i, node := range nodes {
			log.Printf("Node %v is at loop %v", node.Name, i)
			//TODO: Error handling if annotation non exist
			renewableShare, err := strconv.ParseFloat(node.Annotations["renewable"], 32)
			if err != nil {
				log.Printf("error running program: %s \n", err.Error())
			}
			score = int(renewableShare)
			nodeScoreMap[node.Name] = score
		}

		// pods on worst node get evicted
		// pod per pod eviction? evict only if enough pods are available?
		// https://stackoverflow.com/questions/62803041/how-to-evict-or-delete-pods-from-kubernetes-using-golang-client

		// Or specify namespace to get pods in particular namespace
		pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			panic(err.Error())
		}
		log.Printf("There are %d pods in the cluster\n", len(pods.Items))

		// Examples for error handling:
		// - Use helper functions e.g. errors.IsNotFound()
		// - And/or cast to StatusError and use its properties like e.g. ErrStatus.Message
		_, err = clientset.CoreV1().Pods("default").Get(context.TODO(), "example-xxxxx", metav1.GetOptions{})
		if errors.IsNotFound(err) {
			log.Printf("Pod example-xxxxx not found in default namespace\n")
		} else if statusError, isStatus := err.(*errors.StatusError); isStatus {
			log.Printf("Error getting pod %v\n", statusError.ErrStatus.Message)
		} else if err != nil {
			panic(err.Error())
		} else {
			log.Printf("Found example-xxxxx pod in default namespace\n")
		}

		time.Sleep(time.Duration(interval) * time.Second)
	}
}

func getConfig() int {
	// get descheduling interval / other variables (Threshold?) from config
	var env_interval string = os.Getenv("DESCHEDULING_INTERVAL")

	interval, err := strconv.Atoi(env_interval)
	if err == nil {
		log.Printf("The current descheduling interval is %v", interval)
	}

	return interval
}
