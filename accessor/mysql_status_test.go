package accessor

import (
	"context"
	"strconv"
	"time"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	host      = "localhost"
	password  = "test-password"
	port      = 3306
	namespace = "test-namespace"
)

var intermediateSecret = "intermediate-primary-secret"

var _ = Describe("Get MySQLCluster status", func() {
	It("should initialize MySQL for testing", func() {
		err := initializeMySQL()
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("should get MySQL status", func() {
		_, inf, cluster := getAccessorInfraCluster()

		logger := ctrl.Log.WithName("controllers").WithName("MySQLCluster")
		sts := GetMySQLClusterStatus(context.Background(), logger, inf, &cluster)

		Expect(sts.InstanceStatus).Should(HaveLen(1))
		Expect(sts.InstanceStatus[0].PrimaryStatus).ShouldNot(BeNil())
		Expect(sts.InstanceStatus[0].ReplicaStatus).ShouldNot(BeNil())
		Expect(sts.InstanceStatus[0].AllRelayLogExecuted).Should(BeTrue())
		Expect(sts.InstanceStatus[0].GlobalVariablesStatus).ShouldNot(BeNil())
		Expect(sts.InstanceStatus[0].CloneStateStatus).ShouldNot(BeNil())
		Expect(*sts.Latest).Should(Equal(0))
	})

	It("should get and validate intermediate primary options", func() {
		_, inf, cluster := getAccessorInfraCluster()
		cluster.Spec.ReplicationSourceSecretName = &intermediateSecret
		err := k8sClient.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
		Expect(err).ShouldNot(HaveOccurred())

		By("setting valid options to api server")
		data := map[string][]byte{
			"PRIMARY_HOST":     []byte("dummy-primary"),
			"PRIMARY_PORT":     []byte("3306"),
			"PRIMARY_USER":     []byte("dummy-user"),
			"PRIMARY_PASSWORD": []byte("dummy-password"),
		}
		var ipSecret corev1.Secret
		ipSecret.ObjectMeta.Name = intermediateSecret
		ipSecret.ObjectMeta.Namespace = namespace
		ipSecret.Data = data
		err = k8sClient.Create(context.Background(), &ipSecret)
		Expect(err).ShouldNot(HaveOccurred())

		By("getting and validating intermediate primary options")
		logger := ctrl.Log.WithName("controllers").WithName("MySQLCluster")
		sts := GetMySQLClusterStatus(context.Background(), logger, inf, &cluster)
		expect := &IntermediatePrimaryOptions{
			PrimaryHost:     "dummy-primary",
			PrimaryPassword: "dummy-password",
			PrimaryPort:     3306,
			PrimaryUser:     "dummy-user",
		}
		Expect(sts.IntermediatePrimaryOptions).Should(Equal(expect))

		By("setting options without PRIMARY_HOST to api server")
		data = map[string][]byte{
			"PRIMARY_PORT": []byte("3306"),
		}
		ipSecret.ObjectMeta.Name = intermediateSecret
		ipSecret.ObjectMeta.Namespace = namespace
		ipSecret.Data = data
		err = k8sClient.Update(context.Background(), &ipSecret)
		Expect(err).ShouldNot(HaveOccurred())

		By("getting and validating intermediate primary options")
		logger = ctrl.Log.WithName("controllers").WithName("MySQLCluster")
		sts = GetMySQLClusterStatus(context.Background(), logger, inf, &cluster)
		Expect(sts.IntermediatePrimaryOptions).Should(BeNil())

		By("setting options without INVALID_OPTION to api server")
		data = map[string][]byte{
			"PRIMARY_HOST":   []byte("dummy-primary"),
			"PRIMARY_PORT":   []byte("3306"),
			"INVALID_OPTION": []byte("invalid"),
		}
		ipSecret.ObjectMeta.Name = intermediateSecret
		ipSecret.ObjectMeta.Namespace = namespace
		ipSecret.Data = data
		err = k8sClient.Update(context.Background(), &ipSecret)
		Expect(err).ShouldNot(HaveOccurred())

		By("getting and validating intermediate primary options")
		logger = ctrl.Log.WithName("controllers").WithName("MySQLCluster")
		sts = GetMySQLClusterStatus(context.Background(), logger, inf, &cluster)
		Expect(sts.IntermediatePrimaryOptions).Should(BeNil())
	})

	It("should get latest instance by comparing GTIDs", func() {
		ctx := context.Background()
		_, inf, cluster := getAccessorInfraCluster()
		db, err := inf.GetDB(ctx, &cluster, 0)
		Expect(err).ShouldNot(HaveOccurred())

		By("comarping empty instances")
		status := []MySQLInstanceStatus{
			{
				PrimaryStatus: &MySQLPrimaryStatus{
					ExecutedGtidSet: "",
				},
			},
			{
				PrimaryStatus: &MySQLPrimaryStatus{
					ExecutedGtidSet: "",
				},
			},
		}
		idx, err := GetLatestInstance(ctx, db, status)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(*idx).Should(Equal(0))

		By("including instance which has empty PrimaryStatus")
		status = []MySQLInstanceStatus{
			{
				PrimaryStatus: nil,
			},
		}
		idx, err = GetLatestInstance(ctx, db, status)
		Expect(err.Error()).Should(Equal("cannot compare retrieved/executed GTIDs"))

		By("comparing the same GTIDs")
		status = []MySQLInstanceStatus{
			{
				PrimaryStatus: &MySQLPrimaryStatus{
					ExecutedGtidSet: "3E11FA47-71CA-11E1-9E33-C80AA9429562:23",
				},
			},
			{
				PrimaryStatus: &MySQLPrimaryStatus{
					ExecutedGtidSet: "3E11FA47-71CA-11E1-9E33-C80AA9429562:23",
				},
			},
		}
		idx, err = GetLatestInstance(ctx, db, status)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(*idx).Should(Equal(0))

		By("comparing the GTIDs")
		status = []MySQLInstanceStatus{
			{
				PrimaryStatus: &MySQLPrimaryStatus{
					ExecutedGtidSet: "3E11FA47-71CA-11E1-9E33-C80AA9429562:23",
				},
			},
			{
				PrimaryStatus: &MySQLPrimaryStatus{
					ExecutedGtidSet: "3E11FA47-71CA-11E1-9E33-C80AA9429562:21-57",
				},
			},
		}
		idx, err = GetLatestInstance(ctx, db, status)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(*idx).Should(Equal(1))

		By("comparing the inconsistent GTIDs")
		status = []MySQLInstanceStatus{
			{
				PrimaryStatus: &MySQLPrimaryStatus{
					ExecutedGtidSet: "3E11FA47-71CA-11E1-9E33-C80AA9429562:20-25",
				},
			},
			{
				PrimaryStatus: &MySQLPrimaryStatus{
					ExecutedGtidSet: "3E11FA47-71CA-11E1-9E33-C80AA9429562:21-57",
				},
			},
		}
		idx, err = GetLatestInstance(ctx, db, status)
		Expect(err.Error()).Should(Equal("cannot compare retrieved/executed GTIDs"))
	})
})

func initializeMySQL() error {
	conf := mysql.NewConfig()
	conf.User = "root"
	conf.Passwd = password
	conf.Net = "tcp"
	conf.Addr = host + ":" + strconv.Itoa(port)
	conf.InterpolateParams = true

	var db *sqlx.DB
	var err error
	for i := 0; i < 10; i++ {
		db, err = sqlx.Connect("mysql", conf.FormatDSN())
		if err == nil {
			break
		}
		time.Sleep(time.Second * 3)
	}
	if err != nil {
		return err
	}

	_, err = db.Exec("CREATE USER IF NOT EXISTS ?@'%' IDENTIFIED BY ?", moco.OperatorAdminUser, password)
	if err != nil {
		return err
	}
	_, err = db.Exec("GRANT ALL ON *.* TO ?@'%' WITH GRANT OPTION", moco.OperatorAdminUser)
	if err != nil {
		return err
	}

	_, err = db.Exec("INSTALL PLUGIN rpl_semi_sync_master SONAME 'semisync_master.so'")
	if err != nil {
		if err.Error() != "Error 1125: Function 'rpl_semi_sync_master' already exists" {
			return err
		}
	}
	_, err = db.Exec("INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so'")
	if err != nil {
		if err.Error() != "Error 1125: Function 'rpl_semi_sync_slave' already exists" {
			return err
		}
	}
	_, err = db.Exec("INSTALL PLUGIN clone SONAME 'mysql_clone.so'")
	if err != nil {
		if err.Error() != "Error 1125: Function 'clone' already exists" {
			return err
		}
	}

	_, err = db.Exec(`CHANGE MASTER TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?`,
		"dummy", 3306, "dummy", "dummy")
	if err != nil {
		return err
	}
	_, err = db.Exec(`CLONE LOCAL DATA DIRECTORY = ?`, "/tmp/"+uuid.NewUUID())
	if err != nil {
		return err
	}

	return nil
}

func getAccessorInfraCluster() (*MySQLAccessor, Infrastructure, mocov1alpha1.MySQLCluster) {
	acc := NewMySQLAccessor(&MySQLAccessorConfig{
		ConnMaxLifeTime:   30 * time.Minute,
		ConnectionTimeout: 3 * time.Second,
		ReadTimeout:       30 * time.Second,
	})
	inf := NewInfrastructure(k8sClient, acc, password, []string{host}, 3306)
	cluster := mocov1alpha1.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			ClusterName: "test-cluster",
			Namespace:   namespace,
			UID:         "test-uid",
		},
		Spec: mocov1alpha1.MySQLClusterSpec{
			Replicas: 1,
		},
	}

	return acc, inf, cluster
}
