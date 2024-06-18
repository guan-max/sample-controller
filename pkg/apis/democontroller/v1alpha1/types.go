package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Demo is a specification for a Demo resource
type Demo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DemoSpec   `json:"spec"`
	Status DemoStatus `json:"status"`
}

// DemoSpec is the spec for a Demo resource
type DemoSpec struct {
	FileImage  string `json:"fileImage"`
	SourcePath string `json:"sourcePath"`
	TargetHost string `json:"targetHost"`
	TargetPath string `json:"targetPath"`
}

// DemoStatus is the status for a Demo resource
type DemoStatus struct {
	CopyStatus      string `json:"copyStatus"`
	CopyDescription string `json:"copyDescription"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// DemoList is a list of Demo resources
type DemoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Demo `json:"items"`
}
