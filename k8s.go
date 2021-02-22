package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/scheme"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"log"
	"strconv"
	"strings"
)

type K8sClient struct {
	client     *rest.RESTClient
	coreClient *corev1client.CoreV1Client
	config     *rest.Config
}

func NewK8sClient(kubeconfig *string) *K8sClient {
	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(fmt.Errorf("failed to build config %w", err))
	}

	if config.GroupVersion == nil {
		config.GroupVersion = &v1.SchemeGroupVersion
	}
	config.NegotiatedSerializer = serializer.WithoutConversionCodecFactory{CodecFactory: scheme.Codecs}
	config.APIPath = "/api"
	config.ContentType = runtime.ContentTypeJSON

	client, err := rest.RESTClientFor(config)

	if err != nil {
		panic(fmt.Errorf("failed to create rest client %w", err))
	}

	return &K8sClient{client, corev1client.New(client), config}
}

func (kc *K8sClient) ListPods(namespace string) *v1.PodList {
	pods, err := kc.coreClient.Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}
	return pods
}

type ContainerProcess struct {
	Pid     int
	Command string
}

func (kc *K8sClient) ListProcesses(pod *v1.Pod, containerName string) []ContainerProcess {
	out := new(bytes.Buffer)
	if err := kc.Exec(
		pod.Name,
		pod.Namespace,
		containerName,
		[]string{"ps", "-ef", "-o", "pid,args"},
		ExecOptions{
			Out: out,
		},
	); err != nil {
		panic(fmt.Errorf("%+v", err))
	}

	output := out.String()
	println(output)
	lines := strings.Split(output, "\n")
	processes := make([]ContainerProcess, 0)
	for _, line := range lines[1:] {
		line = strings.Trim(line, " \t")
		println("line", line, "line")
		if strings.Contains(line, "ps -ef") || line == "" {
			continue
		}
		parts := strings.Split(line, " ")
		println("splitted", strings.Trim(parts[0], " \t\n"), strings.Trim(parts[1], " \t\n"))
		pid, err := strconv.Atoi(strings.Trim(parts[0], " \t\n"))
		if err != nil {
			panic(err)
		}
		processes = append(processes, ContainerProcess{Pid: pid, Command: parts[1]})
	}
	return processes
}

type ExecOptions struct {
	//An input stream to send to stdin of the remote command
	In io.Reader
	//An output stream that stdout is received in
	Out io.Writer
	//An output stream that stderr is received in
	ErrOut io.Writer
}

//Execute a command on the given pod
func (kc *K8sClient) Exec(
	podName string,
	namespace string,
	container string,
	command []string,
	options ...ExecOptions,
) error {
	var opts ExecOptions
	if len(options) > 0 {
		opts = options[0]
	} else {
		opts = ExecOptions{}
	}

	req := kc.client.Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			//Container: container,
			Command: command,
			Stdin:   opts.In != nil,
			Stdout:  opts.Out != nil,
			Stderr:  opts.ErrOut != nil,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(kc.config, "POST", req.URL())
	if err != nil {
		return err
	}
	//return p.Executor.Execute("POST", req.URL(), p.Config, p.In, p.Out, p.ErrOut, t.Raw, sizeQueue)
	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  opts.In,
		Stdout: opts.Out,
		Stderr: opts.ErrOut,
	})

	if err != nil {
		log.Fatal("failed to exec", err)
	}
	return nil
}
