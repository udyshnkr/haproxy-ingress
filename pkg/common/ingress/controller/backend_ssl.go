/*
Copyright 2015 The Kubernetes Authors.

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

package controller

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/jcmoraisjr/haproxy-ingress/pkg/common/ingress"
	"github.com/jcmoraisjr/haproxy-ingress/pkg/common/net/ssl"

	"github.com/golang/glog"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
)

// SyncSecret keeps in sync Secrets used by Ingress rules with the files on
// disk to allow copy of the content of the secret to disk to be used
// by external processes.
func (ic *GenericController) SyncSecret(key string) {
	glog.V(3).Infof("starting syncing of secret %v", key)

	secret, err := ic.newctrl.GetSecret(key)
	if err != nil {
		return
	}

	cert, err := ic.getPemCertificate(secret)
	if err != nil {
		glog.V(3).Infof("syncing a non ca/crt secret %v", key)
		return
	}

	// create certificates and add or update the item in the store
	cur, exists := ic.sslCertTracker.Get(key)
	if exists {
		s := cur.(*ingress.SSLCert)
		if reflect.DeepEqual(s, cert) {
			// no need to update
			return
		}
		glog.Infof("updating secret %v in the local store", key)
		ic.sslCertTracker.Update(key, cert)
		// this update must trigger an update
		// (like an update event from a change in Ingress)
		return
	}

	glog.Infof("adding secret %v to the local store", key)
	ic.sslCertTracker.Add(key, cert)
	// this update must trigger an update
	// (like an update event from a change in Ingress)
}

// getPemCertificate receives a secret, and creates a ingress.SSLCert as return.
// It parses the secret and verifies if it's a keypair, or a 'ca.crt' secret only.
func (ic *GenericController) getPemCertificate(secret *apiv1.Secret) (*ingress.SSLCert, error) {
	secretName := fmt.Sprintf("%v/%v", secret.Namespace, secret.Name)
	cert, okcert := secret.Data[apiv1.TLSCertKey]
	key, okkey := secret.Data[apiv1.TLSPrivateKeyKey]
	ca := secret.Data["ca.crt"]
	nsSecName := strings.Replace(secretName, "/", "_", -1)

	var s *ingress.SSLCert
	var err error
	if okcert && okkey {
		if cert == nil {
			return nil, fmt.Errorf("secret %v has no 'tls.crt'", secretName)
		}
		if key == nil {
			return nil, fmt.Errorf("secret %v has no 'tls.key'", secretName)
		}

		// If 'ca.crt' is also present, it will allow this secret to be used in the
		// 'ingress.kubernetes.io/auth-tls-secret' annotation
		s, err = ssl.AddOrUpdateCertAndKey(nsSecName, cert, key, ca)
		if err != nil {
			return nil, fmt.Errorf("unexpected error creating pem file: %v", err)
		}

		glog.V(3).Infof("found 'tls.crt' and 'tls.key', configuring %v as a TLS Secret (CN: %v)", secretName, s.CN)
		if ca != nil {
			glog.V(3).Infof("found 'ca.crt', secret %v can also be used for Certificate Authentication", secretName)
		}

	} else if ca != nil {
		crl, _ := secret.Data["ca.crl"]
		s, err = ssl.AddCertAuth(nsSecName, ca, crl)

		if err != nil {
			return nil, fmt.Errorf("unexpected error creating pem file: %v", err)
		}

		// makes this secret in 'syncSecret' to be used for Certificate Authentication
		// this does not enable Certificate Authentication
		glog.V(3).Infof("found only 'ca.crt', configuring %v as an Certificate Authentication Secret", secretName)

	} else {
		return nil, fmt.Errorf("no keypair or CA cert could be found in %v", secretName)
	}

	s.Name = secret.Name
	s.Namespace = secret.Namespace
	return s, nil
}

// sslCertTracker holds a store of referenced Secrets in Ingress rules
type sslCertTracker struct {
	cache.ThreadSafeStore
}

func newSSLCertTracker() *sslCertTracker {
	return &sslCertTracker{
		cache.NewThreadSafeStore(cache.Indexers{}, cache.Indices{}),
	}
}

func (s *sslCertTracker) DeleteAll(key string) {
	s.Delete(key)
}
