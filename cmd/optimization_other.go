//go:build !linux

package cmd

func ApplyTCPTuning() { logger.Info("Non-Linux system: TCP kernel tuning skipped") }
