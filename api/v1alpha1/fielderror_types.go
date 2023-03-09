/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/util/validation/field"
)

type FieldError struct {
	Type   field.ErrorType `json:"type" protobuf:"bytes,1,opt,name=type"`
	Detail string          `json:"detail" protobuf:"bytes,2,opt,name=detail"`
	//BadValue interface{} `json:"badValue" protobuf:"bytes,3,opt,name=badValue"`
}

type FieldErrors map[string]FieldError

func (f *FieldErrors) SetError(err *field.Error) {
	if f == nil || err == nil {
		return
	}

	(*f)[err.Field] = FieldError{
		Type:   err.Type,
		Detail: err.Detail,
	}
}
