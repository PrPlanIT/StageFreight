package docker

import (
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/output"
)

// renderBuildLayers renders parsed layer events into a Section.
// Returns true if any layers were rendered.
func renderBuildLayers(sec *output.Section, steps []build.StepResult, color bool) bool {
	hasLayers := false
	for _, sr := range steps {
		for _, layer := range sr.Layers {
			instr := build.FormatLayerInstruction(layer)
			timing := build.FormatLayerTiming(layer)

			var label string
			if layer.Instruction == "FROM" {
				label = "base"
			} else {
				label = layer.Instruction
			}

			timingStr := timing
			if layer.Cached {
				timingStr = output.Dimmed("cached", color)
			}
			sec.Row("%-8s%-42s %s", label, instr, timingStr)
			hasLayers = true
		}
	}
	return hasLayers
}
