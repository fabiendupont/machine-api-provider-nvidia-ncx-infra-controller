package baremetalhost

import (
	"context"
	"fmt"
	"sync"
	"time"

	metal3 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	nico "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
	"github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/actuators/machine"
)

const (
	syncInterval = 60 * time.Second
	skuCacheTTL  = 5 * time.Minute
)

// Reconciler syncs NICo machines to BareMetalHost and
// HostFirmwareComponents CRs.
type Reconciler struct {
	client.Client
	NicoClient machine.NicoClientInterface
	OrgName    string
	Namespace  string

	skuCache      map[string]*nico.Sku
	skuCacheExpiry time.Time
	skuMu         sync.Mutex
}

// Reconcile is triggered periodically to sync NICo machines.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Syncing NICo machines to BareMetalHost CRs")

	machines, httpResp, err := r.NicoClient.GetAllMachine(ctx, r.OrgName)
	if err != nil || httpResp == nil || httpResp.StatusCode >= 300 {
		if httpResp != nil && httpResp.StatusCode == 403 {
			logger.Info("Provider-admin access required for machine metadata, skipping BMH sync")
			return ctrl.Result{RequeueAfter: syncInterval}, nil
		}
		logger.Error(err, "Failed to list NICo machines")
		return ctrl.Result{RequeueAfter: syncInterval}, err
	}

	skuMap := r.getSkuMap(ctx)
	firmwareMap := r.getFirmwareMap(ctx)

	for _, m := range machines {
		if m.Id == nil {
			continue
		}
		if err := r.syncMachine(ctx, m, skuMap, firmwareMap); err != nil {
			logger.Error(err, "Failed to sync machine", "machineId", *m.Id)
		}
	}

	return ctrl.Result{RequeueAfter: syncInterval}, nil
}

func (r *Reconciler) syncMachine(
	ctx context.Context,
	m nico.Machine,
	skuMap map[string]*nico.Sku,
	firmwareMap map[string]map[string]string,
) error {
	machineID := derefStr(m.Id)
	sku := skuMap[machineID]

	desired := MachineToBaremetalHost(m, sku, r.Namespace)

	existing := &metal3.BareMetalHost{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if errors.IsNotFound(err) {
		if createErr := r.Create(ctx, desired); createErr != nil {
			return fmt.Errorf("create BMH: %w", createErr)
		}
	} else if err != nil {
		return fmt.Errorf("get BMH: %w", err)
	} else {
		existing.Spec = desired.Spec
		existing.Labels = desired.Labels
		existing.Annotations = desired.Annotations
		if updateErr := r.Update(ctx, existing); updateErr != nil {
			return fmt.Errorf("update BMH: %w", updateErr)
		}
	}

	if fw, ok := firmwareMap[machineID]; ok {
		if err := r.syncFirmwareComponents(ctx, machineID, fw, m); err != nil {
			return fmt.Errorf("sync HFC: %w", err)
		}
	}

	return nil
}

func (r *Reconciler) syncFirmwareComponents(
	ctx context.Context,
	machineID string,
	firmwareVersions map[string]string,
	m nico.Machine,
) error {
	desired := FirmwareToHostFirmwareComponents(machineID, firmwareVersions, m, r.Namespace)

	existing := &metal3.HostFirmwareComponents{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if errors.IsNotFound(err) {
		if createErr := r.Create(ctx, desired); createErr != nil {
			return createErr
		}
		existing = desired
	} else if err != nil {
		return err
	}

	existing.Status.Components = desired.Status.Components
	existing.Status.LastUpdated = &metav1.Time{Time: time.Now()}
	return r.Status().Update(ctx, existing)
}

func (r *Reconciler) getSkuMap(ctx context.Context) map[string]*nico.Sku {
	r.skuMu.Lock()
	defer r.skuMu.Unlock()

	if r.skuCache != nil && time.Now().Before(r.skuCacheExpiry) {
		return r.skuCache
	}

	skus, httpResp, err := r.NicoClient.GetAllSku(ctx, r.OrgName)
	if err != nil || httpResp == nil || httpResp.StatusCode >= 300 {
		return nil
	}

	skuMap := make(map[string]*nico.Sku)
	for i := range skus {
		for _, mid := range skus[i].AssociatedMachineIds {
			skuMap[mid] = &skus[i]
		}
	}

	r.skuCache = skuMap
	r.skuCacheExpiry = time.Now().Add(skuCacheTTL)
	return skuMap
}

func (r *Reconciler) getFirmwareMap(ctx context.Context) map[string]map[string]string {
	endpoints, httpResp, err := r.NicoClient.GetAllSiteExplorerEndpoint(ctx, r.OrgName)
	if err != nil || httpResp == nil || httpResp.StatusCode >= 300 {
		return nil
	}

	fwMap := make(map[string]map[string]string)
	for _, ep := range endpoints {
		if ep.Report == nil {
			continue
		}
		mid := ep.Report.MachineId.Get()
		if mid == nil || *mid == "" {
			continue
		}
		if len(ep.Report.FirmwareVersions) > 0 {
			fwMap[*mid] = ep.Report.FirmwareVersions
		}
	}
	return fwMap
}

// SetupWithManager registers the reconciler. It uses a dummy source
// that triggers periodically since we're polling NICo, not watching CRs.
func SetupWithManager(
	mgr ctrl.Manager,
	nicoClient machine.NicoClientInterface,
	orgName, namespace string,
) error {
	r := &Reconciler{
		Client:    mgr.GetClient(),
		NicoClient: nicoClient,
		OrgName:   orgName,
		Namespace: namespace,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&metal3.BareMetalHost{}).
		Complete(r)
}
