package packet

import (
	proto "github.com/appscode/api/kubernetes/v1beta1"
	"github.com/appscode/errors"
	"github.com/appscode/pharmer/api"
	"github.com/appscode/pharmer/cloud/lib"
	"github.com/appscode/pharmer/contexts"
	"github.com/appscode/pharmer/phid"
	"github.com/appscode/pharmer/util/credentialutil"
)

type clusterManager struct {
	ctx   *contexts.ClusterContext
	ins   *contexts.ClusterInstances
	conn  *cloudConnector
	namer namer
}

func (cm *clusterManager) initContext(req *proto.ClusterCreateRequest) error {
	err := cm.LoadDefaultContext()
	if err != nil {
		return errors.FromErr(err).WithContext(cm.ctx).Err()
	}
	cm.namer = namer{ctx: cm.ctx}

	//cluster.ctx.Name = req.Name
	//cluster.ctx.PHID = phid.NewKubeCluster()
	//cluster.ctx.Provider = req.Provider
	//cluster.ctx.Zone = req.Zone

	cm.ctx.Region = cm.ctx.Zone
	cm.ctx.DoNotDelete = req.DoNotDelete
	lib.SetApps(cm.ctx)

	cm.ctx.SetNodeGroups(req.NodeGroups)
	cm.ctx.Project = cm.ctx.CloudCredential[credentialutil.PacketCredentialProjectID]

	cm.ctx.KubernetesMasterName = cm.namer.MasterName()
	cm.ctx.SSHKey, err = contexts.NewSSHKeyPair()
	if err != nil {
		return errors.FromErr(err).WithContext(cm.ctx).Err()
	}
	cm.ctx.SSHKeyExternalID = cm.namer.GenSSHKeyExternalID()
	cm.ctx.SSHKeyPHID = phid.NewSSHKey()

	lib.GenClusterTokens(cm.ctx)

	cm.ctx.AppsCodeNamespace = cm.ctx.Auth.Namespace

	return nil
}

func (cm *clusterManager) LoadDefaultContext() error {
	err := lib.LoadDefaultGenericContext(cm.ctx)
	if err != nil {
		return errors.FromErr(err).WithContext(cm.ctx).Err()
	}

	cm.ctx.OS = "debian"
	cm.ctx.MasterSKU = "baremetal_0"

	cm.ctx.EnableClusterVPN = ""
	cm.ctx.VpnPsk = ""

	lib.BuildRuntimeConfig(cm.ctx)
	return nil
}

func (cm *clusterManager) UploadStartupConfig() error {
	if api.UseFirebase() {
		return lib.UploadStartupConfigInFirebase(cm.ctx)
	}
	return nil
}
