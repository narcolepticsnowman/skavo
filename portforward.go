//mostly lifted from https://github.com/kubernetes/kubectl/blob/38408eaa8c815840f6f214e1176d0078752c1514/pkg/cmd/portforward/portforward.go

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes/scheme"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/polymorphichelpers"
	"k8s.io/kubectl/pkg/util"

	util2 "github.com/ncsnw/skavo/pkg/util"
)

// PortForwardOptions contains all the options for running the port-forward cli command.
type PortForwardOptions struct {
	Namespace     string
	PodName       string
	RESTClient    *restclient.RESTClient
	Config        *restclient.Config
	PodClient     corev1client.PodsGetter
	Address       []string
	Ports         []string
	PortForwarder portForwarder
	StopChannel   chan struct{}
	ReadyChannel  chan struct{}
}

type portForwarder interface {
	ForwardPorts(method string, url *url.URL, opts PortForwardOptions) error
}

type defaultPortForwarder struct {
	genericclioptions.IOStreams
}

func (f *defaultPortForwarder) ForwardPorts(method string, url *url.URL, opts PortForwardOptions) error {
	transport, upgrader, err := spdy.RoundTripperFor(opts.Config)
	util2.MaybePanic(err)
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, method, url)
	fw, err := portforward.NewOnAddresses(dialer, opts.Address, opts.Ports, opts.StopChannel, opts.ReadyChannel, f.Out, f.ErrOut)
	util2.MaybePanic(err)
	return fw.ForwardPorts()
}

// splitPort splits port string which is in form of [LOCAL PORT]:REMOTE PORT
// and returns local and remote ports separately
func splitPort(port string) (local, remote string) {
	parts := strings.Split(port, ":")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}

	return parts[0], parts[0]
}

// Translates service port to target port
// It rewrites ports as needed if the Service port declares targetPort.
// It returns an error when a named targetPort can't find a match in the pod, or the Service did not declare
// the port.
func translateServicePortToTargetPort(ports []string, svc corev1.Service, pod corev1.Pod) ([]string, error) {
	var translated []string
	for _, port := range ports {
		localPort, remotePort := splitPort(port)

		portnum, err := strconv.Atoi(remotePort)
		if err != nil {
			svcPort, err := util.LookupServicePortNumberByName(svc, remotePort)
			if err != nil {
				return nil, err
			}
			portnum = int(svcPort)

			if localPort == remotePort {
				localPort = strconv.Itoa(portnum)
			}
		}
		containerPort, err := util.LookupContainerPortNumberByServicePort(svc, pod, int32(portnum))
		if err != nil {
			// can't resolve a named port, or Service did not declare this port, return an error
			return nil, err
		}

		// convert the resolved target port back to a string
		remotePort = strconv.Itoa(int(containerPort))

		if localPort != remotePort {
			translated = append(translated, fmt.Sprintf("%s:%s", localPort, remotePort))
		} else {
			translated = append(translated, remotePort)
		}
	}
	return translated, nil
}

// convertPodNamedPortToNumber converts named ports into port numbers
// It returns an error when a named port can't be found in the pod containers
func convertPodNamedPortToNumber(ports []string, pod corev1.Pod) ([]string, error) {
	var converted []string
	for _, port := range ports {
		localPort, remotePort := splitPort(port)

		containerPortStr := remotePort
		_, err := strconv.Atoi(remotePort)
		if err != nil {
			containerPort, err := util.LookupContainerPortNumberByName(pod, remotePort)
			if err != nil {
				return nil, err
			}

			containerPortStr = strconv.Itoa(int(containerPort))
		}

		if localPort != remotePort {
			converted = append(converted, fmt.Sprintf("%s:%s", localPort, containerPortStr))
		} else {
			converted = append(converted, containerPortStr)
		}
	}

	return converted, nil
}

func checkUDPPorts(udpOnlyPorts sets.Int, ports []string, obj metav1.Object) error {
	for _, port := range ports {
		_, remotePort := splitPort(port)
		portNum, err := strconv.Atoi(remotePort)
		if err != nil {
			switch v := obj.(type) {
			case *corev1.Service:
				svcPort, err := util.LookupServicePortNumberByName(*v, remotePort)
				if err != nil {
					return err
				}
				portNum = int(svcPort)

			case *corev1.Pod:
				ctPort, err := util.LookupContainerPortNumberByName(*v, remotePort)
				if err != nil {
					return err
				}
				portNum = int(ctPort)

			default:
				return fmt.Errorf("unknown object: %v", obj)
			}
		}
		if udpOnlyPorts.Has(portNum) {
			return fmt.Errorf("UDP protocol is not supported for %s", remotePort)
		}
	}
	return nil
}

// checkUDPPortInService returns an error if remote port in Service is a UDP port
// TODO: remove this check after #47862 is solved
func checkUDPPortInService(ports []string, svc *corev1.Service) error {
	udpPorts := sets.NewInt()
	tcpPorts := sets.NewInt()
	for _, port := range svc.Spec.Ports {
		portNum := int(port.Port)
		switch port.Protocol {
		case corev1.ProtocolUDP:
			udpPorts.Insert(portNum)
		case corev1.ProtocolTCP:
			tcpPorts.Insert(portNum)
		}
	}
	return checkUDPPorts(udpPorts.Difference(tcpPorts), ports, svc)
}

// checkUDPPortInPod returns an error if remote port in Pod is a UDP port
// TODO: remove this check after #47862 is solved
func checkUDPPortInPod(ports []string, pod *corev1.Pod) error {
	udpPorts := sets.NewInt()
	tcpPorts := sets.NewInt()
	for _, ct := range pod.Spec.Containers {
		for _, ctPort := range ct.Ports {
			portNum := int(ctPort.ContainerPort)
			switch ctPort.Protocol {
			case corev1.ProtocolUDP:
				udpPorts.Insert(portNum)
			case corev1.ProtocolTCP:
				tcpPorts.Insert(portNum)
			}
		}
	}
	return checkUDPPorts(udpPorts.Difference(tcpPorts), ports, pod)
}

// Complete completes all the required options for port-forward cmd.
func (o *PortForwardOptions) Complete(f cmdutil.Factory, cmd *cobra.Command, args []string) error {
	var err error
	if len(args) < 2 {
		return cmdutil.UsageErrorf(cmd, "TYPE/NAME and list of ports are required for port-forward")
	}

	o.Namespace, _, err = f.ToRawKubeConfigLoader().Namespace()
	util2.MaybePanic(err)

	builder := f.NewBuilder().
		WithScheme(scheme.Scheme, scheme.Scheme.PrioritizedVersionsAllGroups()...).
		ContinueOnError().
		NamespaceParam(o.Namespace).DefaultNamespace()

	getPodTimeout, err := cmdutil.GetPodRunningTimeoutFlag(cmd)
	if err != nil {
		return cmdutil.UsageErrorf(cmd, err.Error())
	}

	resourceName := args[0]
	builder.ResourceNames("pods", resourceName)

	obj, err := builder.Do().Object()
	util2.MaybePanic(err)

	forwardablePod, err := polymorphichelpers.AttachablePodForObjectFn(f, obj, getPodTimeout)
	util2.MaybePanic(err)

	o.PodName = forwardablePod.Name

	// handle service port mapping to target port if needed
	switch t := obj.(type) {
	case *corev1.Service:
		err = checkUDPPortInService(args[1:], t)
		if err != nil {
			return err
		}
		o.Ports, err = translateServicePortToTargetPort(args[1:], *t, *forwardablePod)
		if err != nil {
			return err
		}
	default:
		err = checkUDPPortInPod(args[1:], forwardablePod)
		if err != nil {
			return err
		}
		o.Ports, err = convertPodNamedPortToNumber(args[1:], *forwardablePod)
		if err != nil {
			return err
		}
	}

	clientset, err := f.KubernetesClientSet()
	util2.MaybePanic(err)

	o.PodClient = clientset.CoreV1()

	o.Config, err = f.ToRESTConfig()
	util2.MaybePanic(err)
	o.RESTClient, err = f.RESTClient()
	util2.MaybePanic(err)

	o.StopChannel = make(chan struct{}, 1)
	o.ReadyChannel = make(chan struct{})
	return nil
}

// Validate validates all the required options for port-forward cmd.
func (o PortForwardOptions) Validate() error {
	if len(o.PodName) == 0 {
		return fmt.Errorf("pod name or resource type/name must be specified")
	}

	if len(o.Ports) < 1 {
		return fmt.Errorf("at least 1 PORT is required for port-forward")
	}

	if o.PortForwarder == nil || o.PodClient == nil || o.RESTClient == nil || o.Config == nil {
		return fmt.Errorf("client, client config, restClient, and portforwarder must be provided")
	}
	return nil
}

// RunPortForward implements all the necessary functionality for port-forward cmd.
func (o PortForwardOptions) RunPortForward() error {
	pod, err := o.PodClient.Pods(o.Namespace).Get(context.TODO(), o.PodName, metav1.GetOptions{})
	util2.MaybePanic(err)

	if pod.Status.Phase != corev1.PodRunning {
		return fmt.Errorf("unable to forward port because pod is not running. Current status=%v", pod.Status.Phase)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)

	go func() {
		<-signals
		if o.StopChannel != nil {
			close(o.StopChannel)
		}
	}()

	req := o.RESTClient.Post().
		Resource("pods").
		Namespace(o.Namespace).
		Name(pod.Name).
		SubResource("portforward")

	return o.PortForwarder.ForwardPorts("POST", req.URL(), o)
}
