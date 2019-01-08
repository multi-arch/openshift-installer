package installconfig

import (
	"os"

	"github.com/ghodss/yaml"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	netopv1 "github.com/openshift/cluster-network-operator/pkg/apis/networkoperator/v1"
	"github.com/openshift/installer/pkg/asset"
	"github.com/openshift/installer/pkg/asset/installconfig/libvirt"
	"github.com/openshift/installer/pkg/ipnet"
	"github.com/openshift/installer/pkg/types"
	openstackvalidation "github.com/openshift/installer/pkg/types/openstack/validation"
	"github.com/openshift/installer/pkg/types/validation"
)

const (
	installConfigFilename           = "install-config.yaml"
	deprecatedInstallConfigFilename = "install-config.yml"
)

var (
	defaultMachineCIDR      = ipnet.MustParseCIDR("10.0.0.0/16")
	defaultServiceCIDR      = ipnet.MustParseCIDR("172.30.0.0/16")
	defaultClusterCIDR      = "10.128.0.0/14"
	defaultHostSubnetLength = 9 // equivalent to a /23 per node
)

// InstallConfig generates the install-config.yaml file.
type InstallConfig struct {
	Config *types.InstallConfig `json:"config"`
	File   *asset.File          `json:"file"`
}

var _ asset.WritableAsset = (*InstallConfig)(nil)

// Dependencies returns all of the dependencies directly needed by an
// InstallConfig asset.
func (a *InstallConfig) Dependencies() []asset.Asset {
	return []asset.Asset{
		&clusterID{},
		&sshPublicKey{},
		&baseDomain{},
		&clusterName{},
		&pullSecret{},
		&platform{},
	}
}

// Generate generates the install-config.yaml file.
func (a *InstallConfig) Generate(parents asset.Parents) error {
	clusterID := &clusterID{}
	sshPublicKey := &sshPublicKey{}
	baseDomain := &baseDomain{}
	clusterName := &clusterName{}
	pullSecret := &pullSecret{}
	platform := &platform{}
	parents.Get(
		clusterID,
		sshPublicKey,
		baseDomain,
		clusterName,
		pullSecret,
		platform,
	)

	a.Config = &types.InstallConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName.ClusterName,
		},
		ClusterID:  clusterID.ClusterID,
		SSHKey:     sshPublicKey.Key,
		BaseDomain: baseDomain.BaseDomain,
		Networking: types.Networking{
			Type:        "OpenshiftSDN",
			MachineCIDR: *defaultMachineCIDR,
			ServiceCIDR: *defaultServiceCIDR,
			ClusterNetworks: []netopv1.ClusterNetwork{
				{
					CIDR:             defaultClusterCIDR,
					HostSubnetLength: uint32(defaultHostSubnetLength),
				},
			},
		},
		PullSecret: pullSecret.PullSecret,
	}

	numberOfMasters := int64(3)
	numberOfWorkers := int64(3)
	switch {
	case platform.AWS != nil:
		a.Config.AWS = platform.AWS
	case platform.Libvirt != nil:
		a.Config.Libvirt = platform.Libvirt
		a.Config.Networking.MachineCIDR = *libvirt.DefaultMachineCIDR
		numberOfMasters = 1
		numberOfWorkers = 1
	case platform.None != nil:
		a.Config.None = platform.None
	case platform.OpenStack != nil:
		a.Config.OpenStack = platform.OpenStack
	default:
		panic("unknown platform type")
	}

	a.Config.Machines = []types.MachinePool{
		{
			Name:     "master",
			Replicas: func(x int64) *int64 { return &x }(numberOfMasters),
		},
		{
			Name:     "worker",
			Replicas: func(x int64) *int64 { return &x }(numberOfWorkers),
		},
	}

	data, err := yaml.Marshal(a.Config)
	if err != nil {
		return errors.Wrap(err, "failed to Marshal InstallConfig")
	}
	a.File = &asset.File{
		Filename: installConfigFilename,
		Data:     data,
	}

	return nil
}

// Name returns the human-friendly name of the asset.
func (a *InstallConfig) Name() string {
	return "Install Config"
}

// Files returns the files generated by the asset.
func (a *InstallConfig) Files() []*asset.File {
	if a.File != nil {
		return []*asset.File{a.File}
	}
	return []*asset.File{}
}

// Load returns the installconfig from disk.
func (a *InstallConfig) Load(f asset.FileFetcher) (found bool, err error) {
	file, err := fetchInstallConfigFile(f)
	if file == nil {
		return false, err
	}

	config := &types.InstallConfig{}
	if err := yaml.Unmarshal(file.Data, config); err != nil {
		return false, errors.Wrapf(err, "failed to unmarshal")
	}

	if err := validation.ValidateInstallConfig(config, openstackvalidation.NewValidValuesFetcher()).ToAggregate(); err != nil {
		return false, errors.Wrapf(err, "invalid %q file", installConfigFilename)
	}

	a.File, a.Config = file, config
	return true, nil
}

func fetchInstallConfigFile(f asset.FileFetcher) (*asset.File, error) {
	names := []string{installConfigFilename, deprecatedInstallConfigFilename}
	for i, name := range names {
		file, err := f.FetchByName(name)
		if err == nil {
			if i != 0 {
				logrus.Warnf("Using deprecated %s file. Use %s instead.", name, names[0])
				file.Filename = names[0]
			}
			return file, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return nil, nil
}
