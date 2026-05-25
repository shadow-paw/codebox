package app

import (
	"codebox/internal/container"
	"codebox/internal/image"
)

// SupportedOrchestrators re-exports container.SupportedOrchestrators
// so internal/cli — which is forbidden from importing domain packages
// directly — can present the values as shell-completion candidates.
// Each call returns a fresh slice; callers may mutate it freely.
func SupportedOrchestrators() []string { return container.SupportedOrchestrators() }

// SupportedOS re-exports image.SupportedOS. See SupportedOrchestrators
// for the rationale and the sharing/copy contract.
func SupportedOS() []string { return image.SupportedOS() }

// SupportedPython re-exports image.SupportedPython. See
// SupportedOrchestrators for the rationale and copy contract.
func SupportedPython() []string { return image.SupportedPython() }

// SupportedNode re-exports image.SupportedNode. See
// SupportedOrchestrators for the rationale and copy contract.
func SupportedNode() []string { return image.SupportedNode() }

// SupportedGolang re-exports image.SupportedGolang. See
// SupportedOrchestrators for the rationale and copy contract.
func SupportedGolang() []string { return image.SupportedGolang() }

// SupportedDotnet re-exports image.SupportedDotnet. See
// SupportedOrchestrators for the rationale and copy contract.
func SupportedDotnet() []string { return image.SupportedDotnet() }
