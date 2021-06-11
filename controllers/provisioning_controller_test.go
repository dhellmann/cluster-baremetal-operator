package controllers

import (
	"context"
	"net"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1 "github.com/openshift/api/config/v1"
	fakeconfigclientset "github.com/openshift/client-go/config/clientset/versioned/fake"
	metal3iov1alpha1 "github.com/openshift/cluster-baremetal-operator/api/v1alpha1"
	"github.com/openshift/cluster-baremetal-operator/provisioning"
)

func setUpSchemeForReconciler() *runtime.Scheme {
	scheme := runtime.NewScheme()
	// we need to add the openshift/api to the scheme to be able to read
	// the infrastructure CR
	utilruntime.Must(configv1.AddToScheme(scheme))
	utilruntime.Must(metal3iov1alpha1.AddToScheme(scheme))
	return scheme
}

func newFakeProvisioningReconciler(scheme *runtime.Scheme, object runtime.Object) *ProvisioningReconciler {
	return &ProvisioningReconciler{
		Client:   fakeclient.NewFakeClientWithScheme(scheme, object),
		Scheme:   scheme,
		OSClient: fakeconfigclientset.NewSimpleClientset(),
	}
}

func TestIsEnabled(t *testing.T) {
	testCases := []struct {
		name          string
		infra         *configv1.Infrastructure
		expectedError bool
		isEnabled     bool
	}{
		{
			name: "BaremetalPlatform",
			infra: &configv1.Infrastructure{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Infrastructure",
					APIVersion: "config.openshift.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.InfrastructureStatus{
					Platform: configv1.BareMetalPlatformType,
				},
			},
			expectedError: false,
			isEnabled:     true,
		},
		{
			name: "NoPlatform",
			infra: &configv1.Infrastructure{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Infrastructure",
					APIVersion: "config.openshift.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.InfrastructureStatus{
					Platform: "",
				},
			},
			expectedError: false,
			isEnabled:     false,
		},
		{
			name:          "BadPlatform",
			infra:         &configv1.Infrastructure{},
			expectedError: true,
			isEnabled:     false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Testing tc : %s", tc.name)

			reconciler := ProvisioningReconciler{
				OSClient: fakeconfigclientset.NewSimpleClientset(tc.infra),
			}
			enabled, err := reconciler.isEnabled()
			if tc.expectedError && err == nil {
				t.Error("should have produced an error")
				return
			}
			if !tc.expectedError && err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			assert.Equal(t, tc.isEnabled, enabled, "enabled results did not match")
		})
	}
}

func TestProvisioning(t *testing.T) {
	testCases := []struct {
		name           string
		baremetalCR    *metal3iov1alpha1.Provisioning
		expectedError  bool
		expectedConfig bool
	}{
		{
			name: "ValidCR",
			baremetalCR: &metal3iov1alpha1.Provisioning{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Provisioning",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: metal3iov1alpha1.ProvisioningSingletonName,
				},
			},
			expectedError:  false,
			expectedConfig: true,
		},
		{
			name:           "MissingCR",
			baremetalCR:    &metal3iov1alpha1.Provisioning{},
			expectedError:  false,
			expectedConfig: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Testing tc : %s", tc.name)

			reconciler := newFakeProvisioningReconciler(setUpSchemeForReconciler(), tc.baremetalCR)
			baremetalconfig, err := reconciler.readProvisioningCR(context.TODO())
			if !tc.expectedError && err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			assert.Equal(t, tc.expectedConfig, baremetalconfig != nil, "baremetal config results did not match")
			return
		})
	}
}

func TestNetworkStack(t *testing.T) {
	tests := []struct {
		name    string
		ips     []net.IP
		want    provisioning.NetworkStackType
		wantErr bool
	}{
		{
			name: "v4 basic",
			ips:  []net.IP{net.ParseIP("192.168.0.1")},
			want: provisioning.NetworkStackV4,
		},
		{
			name: "v4 in v6 format: basic",
			ips:  []net.IP{net.ParseIP("::FFFF:192.168.0.1")},
			want: provisioning.NetworkStackV4,
		},
		{
			name: "v6: basic",
			ips:  []net.IP{net.ParseIP("2001:db8::68")},
			want: provisioning.NetworkStackV6,
		},
		{
			name: "dual: basic",
			ips:  []net.IP{net.ParseIP("2001:db8::68"), net.ParseIP("192.168.0.1")},
			want: provisioning.NetworkStackDual,
		},
		{
			name: "v6: with v4 local",
			ips:  []net.IP{net.ParseIP("2001:db8::68"), net.ParseIP("127.0.0.1")},
			want: provisioning.NetworkStackV6,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := networkStack(tt.ips)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("networkStack() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIServerInternalHost(t *testing.T) {
	infra := &configv1.Infrastructure{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Infrastructure",
			APIVersion: "config.openshift.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Status: configv1.InfrastructureStatus{
			APIServerInternalURL: "https://api-int.ostest.test.metalkube.org:6443",
		},
	}
	want := "api-int.ostest.test.metalkube.org"

	r := &ProvisioningReconciler{
		Scheme:   setUpSchemeForReconciler(),
		OSClient: fakeconfigclientset.NewSimpleClientset(infra),
	}
	got, err := r.apiServerInternalHost(context.TODO())
	if err != nil {
		t.Errorf("ProvisioningReconciler.apiServerInternalHost() error = %v", err)
		return
	}
	if got != want {
		t.Errorf("ProvisioningReconciler.apiServerInternalHost() = %v, want %v", got, want)
	}
}
