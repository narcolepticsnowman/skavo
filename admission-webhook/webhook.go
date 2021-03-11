package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"reflect"
	"strings"

	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/klog/v2"
)

func errorResponse(err error) *v1beta1.AdmissionResponse {
	return &v1beta1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}

func getAnnotations(obj runtime.Object) map[string]string {
	metaField := reflect.Indirect(reflect.ValueOf(obj)).FieldByName("ObjectMeta")
	if !metaField.CanAddr() {
		return make(map[string]string)
	}
	return metaField.Interface().(*metav1.ObjectMeta).GetAnnotations()
}

func updatePodSpec(annotations map[string]string, spec corev1.PodSpec) (corev1.PodSpec, error) {
	var container *corev1.Container
	for i := 0; i < len(spec.Containers); i++ {
		if spec.Containers[i].Name == annotations["skavo.container"] {
			container = &spec.Containers[i]
			break
		}
	}
	if container == nil {
		return corev1.PodSpec{}, fmt.Errorf("expected container %s not found in PodSpec %+v", container, spec)
	}

	container.Command = []string{annotations["skavo.cmd"]}
	container.Args = strings.Split(annotations["skavo.args"], " ")
	configMapName := annotations["skavo.cfgMap"]
	var mode int32 = 0o755
	optional := false

	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      configMapName,
		ReadOnly:  true,
		MountPath: annotations["skavo.cmd"],
		SubPath:   annotations["skavo.cmd"],
	})
	spec.Volumes = append(spec.Volumes, corev1.Volume{
		Name: configMapName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: configMapName,
				},
				DefaultMode: &mode,
				Optional:    &optional,
			},
		},
	})

	return spec, nil
}

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

func admit(ar v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {
	obj := ar.Request.Object.Object
	annotations := getAnnotations(obj)
	reviewResponse := v1beta1.AdmissionResponse{}
	reviewResponse.Allowed = true
	if _, ok := annotations["skavo.cmd"]; ok {
		r := reflect.ValueOf(obj)
		specField := reflect.Indirect(r).FieldByName("Spec").FieldByName("Template").FieldByName("Spec")
		updatedPodSpec, err := updatePodSpec(annotations, specField.Interface().(corev1.PodSpec))
		if err != nil {
			return errorResponse(err)
		}
		specField.Set(reflect.ValueOf(updatedPodSpec))
		patch := []patchOperation{
			{
				Op:    "replace",
				Path:  "/spec/template/spec",
				Value: updatedPodSpec,
			},
		}
		reviewResponse.Patch, err = json.Marshal(patch)
		if err != nil {
			return errorResponse(err)
		}
		pt := v1beta1.PatchTypeJSONPatch
		reviewResponse.PatchType = &pt
	}

	return &reviewResponse
}

func main() {

	codecs := serializer.NewCodecFactory(runtime.NewScheme())
	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			if data, err := ioutil.ReadAll(r.Body); err == nil {
				body = data
			}
		}

		// verify the content type is accurate
		contentType := r.Header.Get("Content-Type")
		if contentType != "application/json" {
			klog.Errorf("contentType=%s, expect application/json", contentType)
			return
		}

		klog.V(2).Info(fmt.Sprintf("handling request: %s", body))

		// The AdmissionReview that was sent to the webhook
		requestedAdmissionReview := v1beta1.AdmissionReview{}

		// The AdmissionReview that will be returned
		responseAdmissionReview := v1beta1.AdmissionReview{}

		deserializer := codecs.UniversalDeserializer()
		if _, _, err := deserializer.Decode(body, nil, &requestedAdmissionReview); err != nil {
			klog.Error(err)
			responseAdmissionReview.Response = errorResponse(err)
		} else {
			responseAdmissionReview.Response = admit(requestedAdmissionReview)
		}

		// Return the same UID
		responseAdmissionReview.Response.UID = requestedAdmissionReview.Request.UID

		klog.V(2).Info(fmt.Sprintf("sending response: %v", responseAdmissionReview.Response))

		respBytes, err := json.Marshal(responseAdmissionReview)
		if err != nil {
			klog.Error(err)
		}
		if _, err := w.Write(respBytes); err != nil {
			klog.Error(err)
		}
	})

	server := http.Server{
		Addr:    ":8443",
		Handler: mux,
	}
	log.Fatal(server.ListenAndServeTLS("/tls/cert", "/tls/key"))

}
