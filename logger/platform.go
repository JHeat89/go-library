package logger

import "os"

// Platform identifies the infrastructure the application is running on.
type Platform string

const (
	PlatformLambda     Platform = "lambda"
	PlatformECS        Platform = "ecs"
	PlatformKubernetes Platform = "kubernetes"
	PlatformContainer  Platform = "container"
	PlatformLocal      Platform = "local"
)

// DetectPlatform inspects the environment to determine where the process is
// running. Detection order matters: Lambda and ECS set distinctive variables,
// Kubernetes injects service discovery variables, and a bare container is
// identified by /.dockerenv. Anything else is treated as local.
func DetectPlatform() Platform {
	switch {
	case os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "":
		return PlatformLambda
	case os.Getenv("ECS_CONTAINER_METADATA_URI_V4") != "" || os.Getenv("ECS_CONTAINER_METADATA_URI") != "":
		return PlatformECS
	case os.Getenv("KUBERNETES_SERVICE_HOST") != "":
		return PlatformKubernetes
	case fileExists("/.dockerenv") || os.Getenv("container") != "":
		return PlatformContainer
	default:
		return PlatformLocal
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// platformAttrs returns platform-specific fields worth having on every log
// line when triaging, e.g. which Lambda function and memory size, or which
// AWS region.
func platformAttrs(p Platform) []any {
	var attrs []any
	if region := firstEnv("AWS_REGION", "AWS_DEFAULT_REGION"); region != "" {
		attrs = append(attrs, "region", region)
	}
	switch p {
	case PlatformLambda:
		if fn := os.Getenv("AWS_LAMBDA_FUNCTION_NAME"); fn != "" {
			attrs = append(attrs, "functionName", fn)
		}
		if mem := os.Getenv("AWS_LAMBDA_FUNCTION_MEMORY_SIZE"); mem != "" {
			attrs = append(attrs, "functionMemoryMB", mem)
		}
	case PlatformKubernetes:
		if pod := os.Getenv("HOSTNAME"); pod != "" {
			attrs = append(attrs, "pod", pod)
		}
	}
	return attrs
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}
