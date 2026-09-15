//go:build !windows

package main

import "os/exec"

// Fora do Windows o SysProcAttr não tem prioridade, e baixá-la depois do Start
// deixaria o começo do processo na normal. Quem quiser o daemon inteiro mais
// leve roda `nice whatsapp-reader serve`: os filhos herdam.
func prepararFilho(*exec.Cmd) {}
