package cleaner

import (
	"context"
	"strings"

	"github.com/go-logr/logr"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/loadbalancer/v2/loadbalancers"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/layer3/floatingips"
	capo "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha4"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/networking"
	"sigs.k8s.io/cluster-api-provider-openstack/pkg/cloud/services/provider"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/microerror"
)

type LoadBalancerCleaner struct {
	cli client.Client
}

func NewLoadBalancerCleaner(cli client.Client) *LoadBalancerCleaner {
	return &LoadBalancerCleaner{cli: cli}
}

// force implementing Cleaner interface
var _ Cleaner = &LoadBalancerCleaner{}

func (lbc *LoadBalancerCleaner) Clean(ctx context.Context, log logr.Logger, oc *capo.OpenStackCluster, clusterTag string) error {
	log = log.WithName("LoadBalancerCleaner")

	providerClient, opts, err := provider.NewClientFromCluster(ctx, lbc.cli, oc)
	if err != nil {
		return microerror.Mask(err)
	}

	loadbalancerClient, err := openstack.NewLoadBalancerV2(providerClient, gophercloud.EndpointOpts{
		Region: opts.RegionName,
	})
	if err != nil {
		return microerror.Mask(err)
	}

	allPages, err := loadbalancers.List(loadbalancerClient, loadbalancers.ListOpts{}).AllPages()
	if err != nil {
		return microerror.Mask(err)
	}

	lbList, err := loadbalancers.ExtractLoadBalancers(allPages)
	if err != nil {
		return microerror.Mask(err)
	}

	networkingService, err := networking.NewService(providerClient, opts, log)
	if err != nil {
		return microerror.Mask(err)
	}

	for _, lb := range lbList {
		if !mustBeDeleted(lb, clusterTag) {
			continue
		}

		deleteOpts := loadbalancers.DeleteOpts{
			Cascade: true,
		}

		if lb.VipPortID != "" {
			fip, err := networkingService.GetFloatingIPByPortID(lb.VipPortID)
			if err != nil {
				return microerror.Mask(err)
			}

			if fip != nil && fip.FloatingIP != "" {
				log.Info("Cleaning floating IP", "ip", fip.FloatingIP, "loadbalancer", lb.ID)
				err = lbc.cleanFloatingIP(networkingService, oc, fip)
				if err != nil {
					return microerror.Mask(err)
				}
			}
		}

		log.Info("Cleaning load balancer", "id", lb.ID)
		err = loadbalancers.Delete(loadbalancerClient, lb.ID, deleteOpts).ExtractErr()
		if err != nil {
			return microerror.Mask(err)
		}
	}

	return nil
}

func (lbc *LoadBalancerCleaner) cleanFloatingIP(ns *networking.Service, oc *capo.OpenStackCluster, fip *floatingips.FloatingIP) error {
	if err := ns.DisassociateFloatingIP(oc, fip.FloatingIP); err != nil {
		return microerror.Mask(err)
	}
	if err := ns.DeleteFloatingIP(oc, fip.FloatingIP); err != nil {
		return microerror.Mask(err)
	}
	return nil
}

func mustBeDeleted(lb loadbalancers.LoadBalancer, clusterTag string) bool {
	prefix := "kube_service_" + clusterTag
	for _, tag := range lb.Tags {
		if strings.HasPrefix(tag, prefix) {
			return true
		}
	}
	return false
}