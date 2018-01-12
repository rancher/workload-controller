package endpoint

import (
	"context"

	"strings"

	"fmt"

	"github.com/pkg/errors"
	"github.com/rancher/types/apis/core/v1"
	"github.com/rancher/types/config"
	"github.com/rancher/workload-controller/controller/dnsrecord"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
)

// This controller is responsible for monitoring Endpionts
// finding out if they are the part of DNSRecord service
// and calling the update on the target service

type Controller struct {
	serviceController v1.ServiceController
	serviceLister     v1.ServiceLister
}

func Register(ctx context.Context, workload *config.WorkloadContext) {
	c := &Controller{
		serviceController: workload.Core.Services("").Controller(),
		serviceLister:     workload.Core.Services("").Controller().Lister(),
	}
	workload.Core.Endpoints("").AddLifecycle(c.GetName(), c)
}

func (c *Controller) Remove(obj *corev1.Endpoints) (*corev1.Endpoints, error) {
	return nil, c.reconcileServiceForEndpoint(obj, false)
}

func (c *Controller) Create(obj *corev1.Endpoints) (*corev1.Endpoints, error) {
	return nil, c.reconcileServiceForEndpoint(obj, true)
}

func (c *Controller) Updated(obj *corev1.Endpoints) (*corev1.Endpoints, error) {
	return nil, c.reconcileServiceForEndpoint(obj, false)
}

func (c *Controller) getDNSRecordServices(obj *corev1.Endpoints, create bool) ([]string, error) {
	var nsSvcNames []string
	epNsSvcName := fmt.Sprintf("%s:%s", obj.Namespace, obj.Name)
	if create {
		// figure out DNSRecord services in reverse way
		svcs, err := c.serviceLister.List(obj.Namespace, labels.NewSelector())
		if err != nil {
			return nsSvcNames, err
		}
		for _, svc := range svcs {
			if svc.Annotations == nil {
				continue
			}
			value, ok := svc.Annotations[dnsrecord.DNSAnnotation]
			if !ok {
				continue
			}

			records := strings.Split(value, ",")
			for _, record := range records {
				if strings.TrimSpace(record) == epNsSvcName {
					nsSvcNames = append(nsSvcNames, fmt.Sprintf("%s:%s", svc.Namespace, svc.Name))
				}
			}
		}
	} else {
		// fetch from annotation assuming it was set by service controller
		if value, ok := obj.Annotations[dnsrecord.DNSEndpointAnnotation]; ok {
			nsSvcNames = strings.Split(value, ",")
		}
	}
	return nsSvcNames, nil
}

func (c *Controller) reconcileServiceForEndpoint(obj *corev1.Endpoints, create bool) error {
	namespaceServiceNames, err := c.getDNSRecordServices(obj, create)
	if err != nil {
		return errors.Wrapf(err, "Failed to fetch DNSRecord services for endpoint [%s] in namespace [%s]", obj.Name, obj.Namespace)
	}

	for _, namespaceServiceName := range namespaceServiceNames {
		// fetch the DNSRecord service in the target namespace, and trigger the update
		groomed := strings.TrimSpace(namespaceServiceName)
		splitted := strings.Split(groomed, ":")
		namespace := splitted[0]
		serviceName := splitted[1]

		_, err := c.serviceLister.Get(namespace, serviceName)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				logrus.Infof("DNSRecord service [%s] is not found in namespace [%s]", serviceName, namespace)
				return nil
			}
			return err
		}
		// update with no changes to trigger service controller reconcilation
		logrus.Infof("Trigger endpoints update for service [%s] in namespace [%s]", serviceName, namespace)
		c.serviceController.Enqueue(namespace, serviceName)
		if err != nil {
			return errors.Wrapf(err, "Failed to trigger endpoints update for service [%s] in namespace [%s]", serviceName, namespace)
		}
	}

	return nil
}

func (c *Controller) GetName() string {
	return "dnsRecordEndpointsController"
}
