package delve

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	regv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	certificatesv1 "k8s.io/api/certificates/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/narcolepticsnowman/go-mirror/mirror"

	"github.com/narcolepticsnowman/skavo/pkg/k8s"
)

const (
	configMapName           = "skavo-entrypoint-sh"
	skavoWebhookName        = "skavo-webhook"
	skavoWebhookServiceName = "skavo-webhook-service"
	skavoWebhookSecretName  = "skavo-webhook-secret"
	skavoNamespace          = "skavo-system"
	skavoServiceAccount     = "skavo-service-account"
	skavoClusterRole        = "skavo-cluster-role"
	skavoClusterRoleBinding = "skavo-cluster-role-binding"
	skavoEntrypointShName   = "/skavoEntrypoint.sh"
)

type PodDelve struct {
	Namespace     string
	PodName       string
	ContainerName string
	Process       k8s.ContainerProcess
	Client        *k8s.Client
	LocalPort     string
	PodPort       string
}

func (pd *PodDelve) InstallDelve() {
	fmt.Println("Installing Delve...")
	pd.runScript(installDelve, "installDelve.sh")
}

func (pd *PodDelve) ForwardPort() {
	fmt.Printf("Forwarding local port %s to remote port %s\n", pd.LocalPort, pd.PodPort)
	<-pd.Client.ForwardPort(pd.Namespace, pd.PodName, pd.LocalPort, pd.PodPort)
}

func (pd *PodDelve) RestartProcess() {
	pd.InstallDelve()
	fmt.Printf("Relaunching pid %d with delve\n", pd.Process.Pid)
	go func() {
		args := append([]string{pd.PodPort, strconv.Itoa(pd.Process.Pid)}, strings.Split(pd.Process.Command, " ")...)
		pd.runScript(delveExec, "delveExec.sh", args...)
	}()
	pd.ForwardPort()
}

func hasRefs(refs []metav1.OwnerReference) bool {
	return refs != nil && len(refs) > 0
}

func (pd *PodDelve) readyCount(kind string, name string) (int32, error) {
	resource := pd.getResource(kind, name)
	switch kind {
	case "Deployment":
		return resource.(*appsv1.Deployment).Status.ReadyReplicas, nil
	case "StatefulSet":
		return resource.(*appsv1.StatefulSet).Status.ReadyReplicas, nil
	case "DaemonSet":
		return resource.(*appsv1.DaemonSet).Status.NumberAvailable, nil
	case "ReplicaSet":
		return resource.(*appsv1.ReplicaSet).Status.ReadyReplicas, nil
	default:
		return 0, fmt.Errorf("unexpected Kind: %s", kind)
	}
}

func (pd *PodDelve) getResource(kind string, name string) runtime.Object {
	var res runtime.Object
	var err error
	switch kind {
	case "Deployment":
		res, err = pd.Client.AppsClient.Deployments(pd.Namespace).Get(context.TODO(), name, metav1.GetOptions{})
	case "StatefulSet":
		res, err = pd.Client.AppsClient.StatefulSets(pd.Namespace).Get(context.TODO(), name, metav1.GetOptions{})
	case "DaemonSet":
		res, err = pd.Client.AppsClient.DaemonSets(pd.Namespace).Get(context.TODO(), name, metav1.GetOptions{})
	case "ReplicaSet":
		res, err = pd.Client.AppsClient.ReplicaSets(pd.Namespace).Get(context.TODO(), name, metav1.GetOptions{})
	default:
		panic(fmt.Errorf("unexpected Kind: %s", kind))
	}
	if err != nil {
		panic(fmt.Errorf("failed to load resource of kind %s with name %s: %+v", kind, name, err))
	}
	return res
}

func (pd *PodDelve) GetResource(kind string, name string) {
	pd.interfaceFor(kind, name)
}

func objectName(r runtime.Object) string {
	return mirror.Reflect(r).GetPath("Name").Value().String()
}

func (pd *PodDelve) interfaceFor(kind string, namespace string) *mirror.Reflection {
	var client interface{} = pd.Client.AppsClient
	if kind == "Pod" {
		client = pd.Client.CoreClient
	}
	return mirror.Reflect(client).GetPath(kind + "s").Exec(namespace).Ret()[0]
}

func (pd *PodDelve) UpdateResource(obj runtime.Object) runtime.Object {
	reflector := mirror.Reflect(obj)
	kind := reflector.GetPath("/Kind").Value().String()
	ret := pd.interfaceFor(kind, pd.Namespace).GetPath("Update").Exec(context.TODO(), obj, metav1.UpdateOptions{}).Ret()
	err := ret[1].Value().Interface().(error)
	if err != nil {
		panic(fmt.Errorf("failed to update resource of kind %s: %+v", kind, err))
	}
	return ret[0].Value().Interface().(runtime.Object)
}

func (pd *PodDelve) Relaunch(pod *v1.Pod) {
	kind, resource := pd.getRootResource(pod)
	pd.deployAdmissionWebhook()

	pd.addSkavoAnnotations(resource)
	resource = pd.UpdateResource(resource)
	if kind != "Pod" {
		cnt, err := pd.readyCount(kind, objectName(resource))
		for cnt < 1 {
			if err != nil {
				panic(fmt.Errorf("failed to check read count: %+v", err))
			}
			time.Sleep(1 * time.Second)
			cnt, err = pd.readyCount(kind, objectName(resource))
		}
		resource = pd.getResource(kind, objectName(resource))

		selector, err := metav1.LabelSelectorAsSelector(mirror.Reflect(resource).GetPath("/Spec/Selector").Value().Interface().(*metav1.LabelSelector))
		if err != nil {
			panic(fmt.Errorf("failed to make selector: %+v", err))
		}
		podList, err := pd.Client.CoreClient.Pods(pd.Namespace).List(context.TODO(), metav1.ListOptions{
			TypeMeta:      metav1.TypeMeta{},
			LabelSelector: selector.String(),
			Limit:         1,
		})
		if err != nil {
			panic(fmt.Errorf("failed to get pod list: %+v", err))
		}
		pd.PodName = podList.Items[0].Name
	}
	pd.ForwardPort()
}

func (pd *PodDelve) addSkavoAnnotations(resource runtime.Object) {
	meta := mirror.Reflect(resource).GetPath("/ObjectMeta").Value().Interface().(metav1.ObjectMeta)
	annotations := meta.GetAnnotations()
	annotations["skavo.container"] = pd.ContainerName
	annotations["skavo.cmd"] = skavoEntrypointShName
	annotations["skavo.args"] = pd.PodPort + " " + pd.Process.Command
	annotations["skavo.cfgMap"] = configMapName
	meta.SetAnnotations(annotations)
}

func (pd *PodDelve) AttachToProcess() {
	pd.InstallDelve()
	fmt.Printf("Attaching to Process: %+v\n", pd.Process)
	go func() {
		pd.runScript(delveAttach, "delveAttach.sh", pd.PodPort, strconv.Itoa(pd.Process.Pid))
	}()
	pd.ForwardPort()
}

func (pd *PodDelve) Exec(cmd ...string) {
	pd.Client.Exec(
		pd.PodName,
		pd.Namespace,
		pd.ContainerName,
		cmd,
		k8s.ExecOptions{
			Out:    os.Stdout,
			In:     nil,
			ErrOut: os.Stderr,
		},
	)
}

func (pd *PodDelve) ExecWrite(in io.Reader, cmd ...string) {
	pd.Client.Exec(
		pd.PodName,
		pd.Namespace,
		pd.ContainerName,
		cmd,
		k8s.ExecOptions{
			Out:    nil,
			In:     in,
			ErrOut: nil,
		},
	)
}

func (pd *PodDelve) runScript(src string, name string, args ...string) {
	pd.ExecWrite(strings.NewReader(src), "sh", "-c", "cat /dev/stdin > /"+name)
	pd.Exec("sh", "-c", "sh /"+name+" "+strings.Join(args, " ")+" 2>&1 &")
}

func (pd *PodDelve) getRootResource(pod *v1.Pod) (string, runtime.Object) {
	var root runtime.Object
	kind := ""
	root = pod
	var ownerRef *metav1.OwnerReference
	if hasRefs(pod.OwnerReferences) {
		ownerRef = &pod.OwnerReferences[0]
		for ownerRef != nil {
			kind = ownerRef.Kind
			owner := pd.getResource(ownerRef.Kind, ownerRef.Name)
			switch ownerRef.Kind {
			case "Deployment":
				fallthrough
			case "StatefulSet":
				fallthrough
			case "DaemonSet":
				root = owner
				ownerRef = nil
			case "ReplicaSet":
				if hasRefs(owner.(*appsv1.ReplicaSet).OwnerReferences) {
					ownerRef = &owner.(*appsv1.ReplicaSet).OwnerReferences[0]
				} else {
					root = owner
					ownerRef = nil
				}
			default:
				panic(fmt.Errorf("unexpected owner kind:%s", ownerRef.Kind))
			}
		}
	}
	return kind, root
}

func (pd *PodDelve) createSkavoNamespace() {
	ns, err := pd.Client.CoreClient.Namespaces().Get(context.TODO(), skavoNamespace, metav1.GetOptions{})
	if err != nil || ns == nil {
		_, err := pd.Client.CoreClient.Namespaces().Get(context.TODO(), skavoNamespace, metav1.GetOptions{})
		if err != nil {
			panic("failed to create skavo namespace")
		} else {
			fmt.Printf("Created Namespace %s", skavoNamespace)
		}
	}
}

func (pd *PodDelve) createEntryPointConfigMap() *v1.ConfigMap {
	configMap, err := pd.Client.CoreClient.ConfigMaps(skavoNamespace).Get(context.TODO(), configMapName, metav1.GetOptions{})
	if err != nil || configMap == nil {
		caKey, caCert, err := GenerateKeyAndCert(skavoNamespace, true)
		tlsKey, tlsCert, err := GenerateKeyAndCert(skavoNamespace, false)
		caKeyPem, caCertPem, err := GenerateCertPEMFiles(caCert, caKey, caCert, caKey)
		tlsKeyPem, tlsCertPem, err := GenerateCertPEMFiles(tlsCert, tlsKey, caCert, caKey)
		if err != nil {
			panic(fmt.Errorf("failed to create self signed cert: %+v", err))
		}
		immutable := false
		data := make(map[string]string)
		data[skavoEntrypointShName] = skavoEntrypoint
		binaryData := make(map[string][]byte)
		binaryData["webhook-tls-key"] = tlsKeyPem
		binaryData["webhook-tls-cert"] = tlsCertPem
		binaryData["webhook-ca-key"] = caKeyPem
		binaryData["webhook-ca-cert"] = caCertPem

		configMap = &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: skavoNamespace,
			},
			Immutable:  &immutable,
			Data:       data,
			BinaryData: binaryData,
		}
		configMap, err = pd.Client.CoreClient.ConfigMaps(skavoNamespace).Create(context.TODO(), configMap, metav1.CreateOptions{})
		if err != nil {
			panic(fmt.Errorf("failed to create config map: %+v", err))
		} else {
			fmt.Printf("Created ConfigMap %s", configMapName)
		}
	}
	return configMap
}

func (pd *PodDelve) deployAdmissionWebhook() {
	pd.createSkavoNamespace()
	pd.createEntryPointConfigMap()
	secret := pd.createSignedCertSecret()
	webhook, err := pd.Client.AdmissionClient.MutatingWebhookConfigurations().Get(context.TODO(), skavoWebhookName, metav1.GetOptions{})
	if err != nil || webhook == nil {
		webhook = &regv1.MutatingWebhookConfiguration{
			TypeMeta:   metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{},
			Webhooks: []regv1.MutatingWebhook{
				{
					Name: "",
					//TODO use CSR to have cluster sign generated cert using this method:
					//https://github.com/JoelSpeed/webhook-certificate-generator/blob/master/pkg/certgenerator/run.go
					ClientConfig: regv1.WebhookClientConfig{
						URL:      nil,
						Service:  nil,
						CABundle: secret.Data["webhook-ca-cert"],
					},
					Rules:                   nil,
					FailurePolicy:           nil,
					MatchPolicy:             nil,
					NamespaceSelector:       nil,
					ObjectSelector:          nil,
					SideEffects:             nil,
					TimeoutSeconds:          nil,
					AdmissionReviewVersions: nil,
					ReinvocationPolicy:      nil,
				},
			},
		}
		_, err = pd.Client.AdmissionClient.MutatingWebhookConfigurations().Create(context.TODO(), webhook, metav1.CreateOptions{})
		if err != nil {
			panic(fmt.Errorf("failed to create webhook config: %+v", err))
		} else {
			fmt.Printf("Created ConfigMap %s", configMapName)
		}
	}
}

func (pd *PodDelve) CreateCertSecret() *v1.Secret {
	privateKey := GenerateKey()

	csrPem := CreateCSRPem(skavoNamespace, skavoWebhookServiceName, privateKey)

	csr := &certificatesv1.CertificateSigningRequest{
		TypeMeta: metav1.TypeMeta{Kind: "CertificateSigningRequest"},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s.%s", skavoWebhookServiceName, skavoNamespace),
		},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request: csrPem,
			Usages: []certificatesv1.KeyUsage{
				certificatesv1.UsageDigitalSignature,
				certificatesv1.UsageKeyEncipherment,
				certificatesv1.UsageServerAuth,
			},
		},
	}
	created, err := pd.Client.CertsClient.CertificateSigningRequests().Create(context.TODO(), csr, metav1.CreateOptions{})
	if err != nil {
		panic(fmt.Errorf("failed to create CSR: %+v", err))
	}
	created.Status.Conditions = append(created.Status.Conditions,
		certificatesv1.CertificateSigningRequestCondition{
			Type:           certificatesv1.CertificateApproved,
			Reason:         "SkavoSelfApproved",
			Message:        "Good luck debugging!",
			LastUpdateTime: metav1.Now(),
		},
	)

	_, err = pd.Client.CertsClient.CertificateSigningRequests().UpdateApproval(context.TODO(), created.Name, created, metav1.UpdateOptions{})
	if err != nil {
		panic(fmt.Errorf("failed to approve csr: %+v", err))
	}

	//wait for cert
	_ = wait.PollImmediate(time.Second*2, time.Minute*10, func() (bool, error) {
		csr, err = pd.Client.CertsClient.CertificateSigningRequests().Get(context.TODO(), created.Name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("couldn't get CSR: %v", err)
		}
		if len(csr.Status.Certificate) > 0 {
			return true, nil
		}
		fmt.Printf("Waiting for Certificate...")
		return false, nil
	})

	secret := &v1.Secret{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      skavoWebhookSecretName,
			Namespace: skavoNamespace,
		},
		Data: map[string][]byte{
			"key.pem":  PrivateKeyPem(privateKey),
			"cert.pem": csr.Status.Certificate,
		},
		Type: "",
	}

	createdSecret, err := pd.Client.CoreClient.Secrets(skavoNamespace).Create(context.TODO(), secret, metav1.CreateOptions{})
	if err != nil {
		panic(fmt.Errorf("failed to create secret: %+v", err))
	}
	return createdSecret
}

func (pd *PodDelve) GetSecret(name string) *v1.Secret {
	secret, err := pd.Client.CoreClient.Secrets(skavoNamespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		panic(fmt.Errorf("failed to fetch secret: %+v", err))
	}
	return secret
}

func (pd *PodDelve) writeCABundleToSecret(secretName string) {
	secret := pd.GetSecret(secretName)
	const caBundleKey = "caBundle"
	if _, ok := secret.Data[caBundleKey]; ok {
		return
	}

	serviceAccount := &v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      skavoServiceAccount,
			Namespace: skavoNamespace,
		},
	}
	_, err := pd.Client.CoreClient.ServiceAccounts(skavoNamespace).Create(context.TODO(), serviceAccount, metav1.CreateOptions{})
	if err != nil {
		panic(fmt.Errorf("failed to create service account: %+v", err))
	}

	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: skavoClusterRole,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get", "patch"},
				Resources: []string{"secrets"},
			},
		},
		AggregationRule: nil,
	}

	_, err = pd.Client.RbacClient.ClusterRoles().Create(context.TODO(), clusterRole, metav1.CreateOptions{})
	if err != nil {
		panic(fmt.Errorf("failed to create cluster role: %+v", err))
	}

	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      skavoClusterRoleBinding,
			Namespace: skavoNamespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     skavoClusterRole,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      skavoServiceAccount,
				Namespace: skavoNamespace,
			},
		},
	}

	_, err = pd.Client.RbacClient.ClusterRoleBindings().Create(context.TODO(), clusterRoleBinding, metav1.CreateOptions{})
	if err != nil {
		panic(fmt.Errorf("failed to create clusterRoleBinding"))
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "load-ca-bundle",
			Namespace: skavoNamespace,
		},
		Spec: batchv1.JobSpec{
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ca-bundle-loader",
				},
				Spec: v1.PodSpec{
					ServiceAccountName: skavoServiceAccount,
					Containers: []v1.Container{
						{
							Name:    "",
							Image:   "kubectl",
							Command: []string{"kubectl"},
							Args: []string{"patch", "secret", "-n", skavoNamespace, secretName, "-p",
								"{\"data\":" +
									"{" +
									"\"" + caBundleKey + "\":\"$(base64 < /run/secrets/kubernetes.io/serviceaccount/ca.crt | tr -d '\\n')\"" +
									"}" +
									"}"},
						},
					},
				},
			},
		},
		Status: batchv1.JobStatus{},
	}

	_, err = pd.Client.BatchClient.Jobs(skavoNamespace).Create(context.TODO(), job, metav1.CreateOptions{})

	if err != nil {
		panic(fmt.Errorf("failed to create job: +%v", err))
	}

	secret = pd.GetSecret(secretName)
	_, ok := secret.Data[caBundleKey]
	if ok {
		return
	}
	_ = wait.PollImmediate(time.Second*2, time.Minute*10, func() (bool, error) {
		secret = pd.GetSecret(secretName)
		_, ok = secret.Data[caBundleKey]
		if !ok {
			println("Waiting for caBundle to get loaded...")
		}
		return ok, nil
	})
}
