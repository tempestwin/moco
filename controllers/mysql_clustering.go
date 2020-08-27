package controllers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/go-logr/logr"
	_ "github.com/go-sql-driver/mysql"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reconcileMySQLCluster recoclies MySQL cluster
func (r *MySQLClusterReconciler) reconcileClustering(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (ctrl.Result, error) {
	status := r.MySQLService.GetMySQLClusterStatus(ctx, log, cluster)
	var unavailable bool
	for i, is := range status.InstanceStatus {
		if !is.Available {
			log.Info("unavailable host exists", "index", i)
			unavailable = true
		}
	}
	if unavailable {
		err := r.setFailureCondition(ctx, cluster, errors.New("unavailable host exists"), nil)
		return ctrl.Result{}, err
	}
	log.Info("MySQLClusterStatus", "ClusterStatus", status)

	err := r.validateConstraints(ctx, log, status, cluster)
	if err != nil {
		err = r.setViolationCondition(ctx, cluster, err)
		return ctrl.Result{}, err
	}

	primaryIndex, err := r.selectPrimary(ctx, log, status, cluster)
	if err != nil {
		err = r.setFailureCondition(ctx, cluster, err, nil)
		return ctrl.Result{}, err
	}

	err = r.updatePrimary(ctx, log, status, cluster, primaryIndex)
	if err != nil {
		err = r.setFailureCondition(ctx, cluster, err, nil)
		return ctrl.Result{}, err
	}

	err = r.configureReplication(ctx, log, status, cluster)
	if err != nil {
		err = r.setFailureCondition(ctx, cluster, err, nil)
		return ctrl.Result{}, err
	}

	wait, outOfSyncInts, err := r.waitForReplication(ctx, log, status, cluster)
	if err != nil {
		err = r.setFailureCondition(ctx, cluster, err, nil)
		return ctrl.Result{}, err
	}
	if wait {
		err = r.setUnavailableCondition(ctx, cluster, outOfSyncInts)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	err = r.acceptWriteRequest(ctx, cluster)
	if err != nil {
		err = r.setFailureCondition(ctx, cluster, err, nil)
		return ctrl.Result{}, err
	}
	err = r.setAvailableCondition(ctx, cluster, outOfSyncInts)

	return ctrl.Result{}, err
}

func (r *MySQLClusterReconciler) setFailureCondition(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, e error, outOfSyncInstances []int) error {
	setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionFailure,
		Status:  corev1.ConditionTrue,
		Message: e.Error(),
	})
	setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionAvailable,
		Status:  corev1.ConditionFalse,
		Message: e.Error(),
	})
	setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionHealthy,
		Status:  corev1.ConditionFalse,
		Message: e.Error(),
	})
	if len(outOfSyncInstances) != 0 {
		msg := fmt.Sprintf("outOfSync instances: %#v", outOfSyncInstances)
		setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
			Type:    mocov1alpha1.ConditionOutOfSync,
			Status:  corev1.ConditionTrue,
			Message: msg,
		})
	}

	err := r.Status().Update(ctx, cluster)
	if err != nil {
		return err
	}
	return nil
}

func (r *MySQLClusterReconciler) setViolationCondition(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, e error) error {
	setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionViolation,
		Status:  corev1.ConditionTrue,
		Message: e.Error(),
	})
	setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionFailure,
		Status:  corev1.ConditionTrue,
		Message: e.Error(),
	})
	setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionAvailable,
		Status:  corev1.ConditionFalse,
		Message: e.Error(),
	})
	setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
		Type:    mocov1alpha1.ConditionHealthy,
		Status:  corev1.ConditionFalse,
		Message: e.Error(),
	})

	err := r.Status().Update(ctx, cluster)
	if err != nil {
		return err
	}
	return nil
}

func (r *MySQLClusterReconciler) setUnavailableCondition(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, outOfSyncInstances []int) error {
	if len(outOfSyncInstances) == 0 {
		setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
			Type:   mocov1alpha1.ConditionOutOfSync,
			Status: corev1.ConditionFalse,
		})
	} else {
		msg := fmt.Sprintf("outOfSync instances: %#v", outOfSyncInstances)
		setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
			Type:    mocov1alpha1.ConditionOutOfSync,
			Status:  corev1.ConditionTrue,
			Message: msg,
		})
	}
	setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
		Type:   mocov1alpha1.ConditionHealthy,
		Status: corev1.ConditionFalse,
	})
	setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
		Type:   mocov1alpha1.ConditionAvailable,
		Status: corev1.ConditionFalse,
	})

	err := r.Status().Update(ctx, cluster)
	if err != nil {
		return err
	}
	return nil
}

func (r *MySQLClusterReconciler) setAvailableCondition(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, outOfSyncInstances []int) error {
	if len(outOfSyncInstances) == 0 {
		setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
			Type:   mocov1alpha1.ConditionOutOfSync,
			Status: corev1.ConditionFalse,
		})
		setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
			Type:   mocov1alpha1.ConditionHealthy,
			Status: corev1.ConditionTrue,
		})
	} else {
		msg := fmt.Sprintf("outOfSync instances: %#v", outOfSyncInstances)
		setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
			Type:    mocov1alpha1.ConditionOutOfSync,
			Status:  corev1.ConditionTrue,
			Message: msg,
		})
		setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
			Type:    mocov1alpha1.ConditionHealthy,
			Status:  corev1.ConditionFalse,
			Message: msg,
		})
	}
	setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
		Type:   mocov1alpha1.ConditionAvailable,
		Status: corev1.ConditionTrue,
	})

	err := r.Status().Update(ctx, cluster)
	if err != nil {
		return err
	}
	return nil
}

// MySQLClusterStatus contains MySQLCluster status
type MySQLClusterStatus struct {
	InstanceStatus []MySQLInstanceStatus
}

type MySQLPrimaryStatus struct {
	ExecutedGtidSet sql.NullString `db:"Executed_Gtid_Set"`
}

type MySQLReplicaStatus struct {
	ID               int            `db:"id"`
	LastIoErrno      int            `db:"Last_IO_Errno"`
	LastIoError      sql.NullString `db:"Last_IO_Error"`
	LastSqlErrno     int            `db:"Last_SQL_Errno"`
	LastSqlError     sql.NullString `db:"Last_SQL_Error"`
	MasterHost       string         `db:"Master_Host"`
	RetrievedGtidSet sql.NullString `db:"Retrieved_Gtid_Set"`
	ExecutedGtidSet  sql.NullString `db:"Executed_Gtid_Set"`
	SlaveIoRunning   string         `db:"Slave_IO_Running"`
	SlaveSqlRunning  string         `db:"Slave_SQL_Running"`
}

type MySQLGlobalVariablesStatus struct {
	ReadOnly                           bool `db:"@@read_only"`
	SuperReadOnly                      bool `db:"@@super_read_only"`
	RplSemiSyncMasterWaitForSlaveCount int  `db:"@@rpl_semi_sync_master_wait_for_slave_count"`
}

type MySQLCloneStateStatus struct {
	State sql.NullString `db:"state"`
}

type MySQLInstanceStatus struct {
	Available            bool
	PrimaryStatus        *MySQLPrimaryStatus
	ReplicaStatus        *MySQLReplicaStatus
	GlobalVariableStatus *MySQLGlobalVariablesStatus
	CloneStateStatus     *MySQLCloneStateStatus
}

func (r *MySQLClusterReconciler) validateConstraints(ctx context.Context, log logr.Logger, status *MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) error {
	if status == nil {
		panic("unreachable condition")
	}

	var writableInstanceCounts int
	var primaryIndex int
	for i, status := range status.InstanceStatus {
		if !status.GlobalVariableStatus.ReadOnly {
			writableInstanceCounts++
			primaryIndex = i
		}
	}
	if writableInstanceCounts > 1 {
		return moco.ErrConstraintsViolation
	}

	if cluster.Status.CurrentPrimaryIndex != nil && writableInstanceCounts == 1 {
		if *cluster.Status.CurrentPrimaryIndex != primaryIndex {
			return moco.ErrConstraintsViolation
		}
	}

	cond := findCondition(cluster.Status.Conditions, mocov1alpha1.ConditionViolation)
	if cond != nil {
		return moco.ErrConstraintsRecovered
	}

	return nil
}

func (r *MySQLClusterReconciler) selectPrimary(ctx context.Context, log logr.Logger, status *MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) (int, error) {
	return 0, nil
}

func (r *MySQLClusterReconciler) updatePrimary(ctx context.Context, log logr.Logger, status *MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster, newPrimaryIndex int) error {
	cluster.Status.CurrentPrimaryIndex = &newPrimaryIndex
	err := r.Status().Update(ctx, cluster)
	if err != nil {
		return err
	}

	expectedRplSemiSyncMasterWaitForSlaveCount := int(cluster.Spec.Replicas / 2)
	st := status.InstanceStatus[newPrimaryIndex]
	if st.GlobalVariableStatus.RplSemiSyncMasterWaitForSlaveCount == expectedRplSemiSyncMasterWaitForSlaveCount {
		return nil
	}
	// getTarget
	err = r.MySQLService.SetWaitForSlaveCount(ctx, newPrimaryIndex, expectedRplSemiSyncMasterWaitForSlaveCount)
	return err
}

func (r *MySQLClusterReconciler) configureReplication(ctx context.Context, log logr.Logger, status *MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) error {
	podName := fmt.Sprintf("%s-%d", uniqueName(cluster), *cluster.Status.CurrentPrimaryIndex)
	masterHost := fmt.Sprintf("%s.%s.%s.svc", podName, uniqueName(cluster), cluster.Namespace)
	password, err := getPassword(ctx, r.Client, cluster, moco.ReplicationPasswordKey)
	if err != nil {
		return err
	}

	for i, is := range status.InstanceStatus {
		if i == *cluster.Status.CurrentPrimaryIndex {
			continue
		}
		if is.ReplicaStatus == nil || is.ReplicaStatus.MasterHost != masterHost {
			err := r.MySQLService.StopSlave(ctx, i)
			if err != nil {
				return err
			}
			targetHost, targetPassword, err := getTarget(ctx, r.Client, cluster, i)
			if err != nil {
				return err
			}
			err = r.MySQLService.ChangeMaster(ctx, targetHost, targetPassword, masterHost, moco.MySQLPort, moco.ReplicatorUser, password)
			if err != nil {
				return err
			}
		}
	}

	for i := range status.InstanceStatus {
		if i == *cluster.Status.CurrentPrimaryIndex {
			continue
		}
		err := r.MySQLService.StartSlave(ctx, i)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *MySQLClusterReconciler) waitForReplication(ctx context.Context, log logr.Logger, status *MySQLClusterStatus, cluster *mocov1alpha1.MySQLCluster) (bool, []int, error) {
	primaryIndex := *cluster.Status.CurrentPrimaryIndex
	primaryStatus := status.InstanceStatus[primaryIndex]
	if !primaryStatus.GlobalVariableStatus.ReadOnly {
		return false, nil, nil
	}

	primaryGTID := primaryStatus.PrimaryStatus.ExecutedGtidSet
	count := 0
	var outOfSyncIns []int
	for i, is := range status.InstanceStatus {
		if i == primaryIndex {
			continue
		}

		if is.ReplicaStatus.LastIoErrno != 0 {
			outOfSyncIns = append(outOfSyncIns, i)
			continue
		}

		if is.ReplicaStatus.ExecutedGtidSet == primaryGTID {
			count++
		}
	}

	if count < int(cluster.Spec.Replicas/2) {
		return true, outOfSyncIns, nil
	}
	return false, outOfSyncIns, nil
}

func getPassword(ctx context.Context, c client.Client, cluster *mocov1alpha1.MySQLCluster, passwordKey string) (string, error) {
	secret := &corev1.Secret{}
	myNS, mySecretName := getSecretNameForController(cluster)

	err := c.Get(ctx, client.ObjectKey{Namespace: myNS, Name: mySecretName}, secret)
	if err != nil {
		return "", err
	}
	return string(secret.Data[passwordKey]), nil
}

func (r *MySQLClusterReconciler) acceptWriteRequest(ctx context.Context, cluster *mocov1alpha1.MySQLCluster) error {
	return r.MySQLService.TurnOffReadOnly(ctx, *cluster.Status.CurrentPrimaryIndex)
}

func getTarget(ctx context.Context, cli client.Client, cluster *mocov1alpha1.MySQLCluster, index int) (string, string, error) {
	operatorPassword, err := getPassword(ctx, cli, cluster, moco.OperatorPasswordKey)
	if err != nil {
		return "", "", err
	}

	podName := fmt.Sprintf("%s-%d", uniqueName(cluster), index)
	host := fmt.Sprintf("%s.%s.%s.svc", podName, uniqueName(cluster), cluster.Namespace)

	return host, operatorPassword, nil
}
