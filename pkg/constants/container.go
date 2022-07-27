package constants

// container names
const (
	AgentContainerName             = "agent"
	InitContainerName              = "moco-init"
	InitMySQLDataContainerName     = "moco-init-mysql-data"
	MysqldContainerName            = "mysqld"
	SlowQueryLogAgentContainerName = "slow-log"
	ExporterContainerName          = "mysqld-exporter"
)

// container resources
const (
	AgentContainerCPURequest = "100m"
	AgentContainerCPULimit   = "100m"
	AgentContainerMemRequest = "100Mi"
	AgentContainerMemLimit   = "100Mi"

	InitContainerCPURequest = "100m"
	InitContainerCPULimit   = "100m"
	InitContainerMemRequest = "300Mi"
	InitContainerMemLimit   = "300Mi"

	SlowQueryLogAgentCPURequest = "100m"
	SlowQueryLogAgentCPULimit   = "100m"
	SlowQueryLogAgentMemRequest = "20Mi"
	SlowQueryLogAgentMemLimit   = "20Mi"

	ExporterContainerCPURequest = "200m"
	ExporterContainerCPULimit   = "200m"
	ExporterContainerMemRequest = "100Mi"
	ExporterContainerMemLimit   = "100Mi"
)

// volume names
const (
	MySQLDataVolumeName               = "mysql-data"
	MySQLConfVolumeName               = "mysql-conf"
	MySQLInitConfVolumeName           = "mysql-conf-d"
	MySQLConfSecretVolumeName         = "my-cnf-secret"
	GRPCSecretVolumeName              = "grpc-cert"
	RunVolumeName                     = "run"
	VarLogVolumeName                  = "var-log"
	TmpVolumeName                     = "tmp"
	SlowQueryLogAgentConfigVolumeName = "slow-fluent-bit-config"
)

// UID/GID
const (
	ContainerUID = 27
	ContainerGID = 27
)

// command names
const (
	InitCommand              = "moco-init"
	InitMySQLDataBaseCommand = "mysqld --initialize-insecure"
)

// PreStop sleep duration
const PreStopSeconds = "20"
