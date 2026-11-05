package app

import "github.com/pixingzoudaiyuexing/Narbe/internal/cloud"

func operationProgress(op cloud.Operation, fallback string) string {
	if op.Linode != nil && op.Linode.Stage != "" {
		return op.Linode.Stage
	}
	return fallback
}
