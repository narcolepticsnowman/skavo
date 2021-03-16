package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/homedir"

	"github.com/ncsnw/skavo/pkg/delve"
	"github.com/ncsnw/skavo/pkg/k8s"
	"github.com/ncsnw/skavo/pkg/prompt"
)

func main() {

	kubeconfig := flag.String("kubeconfig", filepath.Join(homedir.HomeDir(), ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	kubeContext := flag.String("context", "", "The kube config context to use")
	podName := flag.String("pod", "", "Specify the pod instead of prompting")
	containerName := flag.String("container", "", "Specify the container instead of prompting")
	processFilter := flag.String("process", "", "Filter the list of processes in a container")
	namespace := flag.String("namespace", "default", "Specify the namespace instead of using default. Use namespace \"ALL\" to view all namespaces")
	isRestart := flag.Bool("restart", false, "Restart the process using delve instead of attaching to the existing process.")
	isRelaunch := flag.Bool("relaunch", false, "Relaunch the pod with delve exec. Warning: this will restart all pods under the parent resource (ReplicaSet, Deployment, etc)")
	localPort := flag.String("localport", "34455", "Specify the host machine port to forward to the pod port")
	podPort := flag.String("podport", "55443", "Specify the pod port for delve to listen on")
	flag.Parse()

	client := k8s.NewK8sClient(*kubeContext, kubeconfig)
	ns := *namespace
	if ns == "ALL" {
		ns = ""
	}
	var pod *v1.Pod
	if *podName == "" {
		podList := client.ListPods(ns)

		pod = prompt.SelectPod(podList.Items)
		fmt.Printf("Selected pod: %s\n", pod.Name)
	} else {
		var err error
		pod, err = client.CoreClient.Pods(ns).Get(context.TODO(), *podName, metav1.GetOptions{})
		if err != nil {
			panic(fmt.Errorf("failed to get pod: %s. %+v", *podName, err))
		}
	}
	if *containerName == "" {
		container := prompt.SelectContainer(pod.Spec.Containers)
		containerName = &container.Name
		fmt.Printf("Selected container: %s\n", container.Name)
	}

	processes := client.ListProcesses(pod, *containerName)

	process := prompt.SelectProcess(processes, *processFilter)
	pd := delve.PodDelve{
		Namespace:     pod.Namespace,
		PodName:       pod.Name,
		ContainerName: *containerName,
		Process:       process,
		Client:        client,
		LocalPort:     *localPort,
		PodPort:       *podPort,
	}
	if *isRestart {
		pd.RestartProcess()
	} else if *isRelaunch {
		pd.Relaunch(pod)
	} else {
		pd.AttachToProcess()
	}

}
