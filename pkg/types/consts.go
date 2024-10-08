package types

const (
	ContainerCommitOnStopEnvFlag = "SEALOS_COMMIT_ON_STOP"
	ImageRegistryAddressOnEnv    = "SEALOS_COMMIT_IMAGE_REGISTRY"
	ImageRegistryUserNameOnEnv   = "SEALOS_COMMIT_IMAGE_REGISTRY_USER"
	ImageRegistryPasswordOnEnv   = "SEALOS_COMMIT_IMAGE_REGISTRY_PASSWORD"
	ImageRegistryRepositoryOnEnv = "SEALOS_COMMIT_IMAGE_REGISTRY_REPOSITORY"
	ImageSquashOnEnv             = "SEALOS_COMMIT_IMAGE_SQUASH"
	ImageNameOnEnv               = "SEALOS_COMMIT_IMAGE_NAME"
	SealosUsernameOnEnv          = "SEALOS_USER"
)
const (
	ImageSquashOnEnvEnableValue         = "true"
	ContainerCommitOnStopEnvEnableValue = "true"
)
