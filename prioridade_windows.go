//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// Transcrever ocupa a CPU por minutos. Em BELOW_NORMAL_PRIORITY_CLASS quem está
// usando o notebook não sente; e CREATE_NO_WINDOW porque o daemon pode rodar
// numa Tarefa Agendada sem console, onde cada ffmpeg e whisper-cli abriria uma
// janela piscando.
func prepararFilho(cmd *exec.Cmd) {
	const belowNormalPriorityClass, createNoWindow = 0x00004000, 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: belowNormalPriorityClass | createNoWindow}
}
