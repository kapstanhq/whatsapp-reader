package transcricao

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrMotorAusente: falta o executável ou o modelo, ou não há serviço no
	// endereço configurado. Não é culpa do áudio, e nenhum outro vai dar certo.
	ErrMotorAusente = errors.New("transcricao: motor ausente ou incompleto")
	// ErrCredencial: o serviço recusou a chave.
	ErrCredencial = errors.New("transcricao: credencial recusada")
	// ErrAudioInvalido: este áudio não vira texto neste motor, por mais que se repita.
	ErrAudioInvalido = errors.New("transcricao: áudio inválido")
	// ErrLimite: o serviço pediu para esperar. O tempo vem em [ErroLimite].
	ErrLimite = errors.New("transcricao: limite de uso atingido")
)

// ErroLimite é um [ErrLimite] com o tempo que o serviço pediu (Retry-After).
type ErroLimite struct {
	Depois time.Duration // zero: o serviço não disse
	Err    error
}

func (e *ErroLimite) Error() string {
	msg := ErrLimite.Error()
	if e.Depois > 0 {
		msg += fmt.Sprintf(" (tente de novo em %s)", e.Depois)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *ErroLimite) Unwrap() error { return e.Err }

func (e *ErroLimite) Is(alvo error) bool { return alvo == ErrLimite }

// Classe diz a uma fila o que fazer com um erro de transcrição.
type Classe int

const (
	Ok Classe = iota
	// Passageiro: tente este áudio de novo mais tarde — rede, limite, prazo estourado.
	Passageiro
	// Definitivo: este áudio não vai dar; os outros seguem.
	Definitivo
	// Configuracao: nenhum áudio vai dar até alguém mexer na instalação ou na
	// chave. Não gaste tentativas: pare e avise.
	Configuracao
	// Cancelado: quem chamou desistiu. Não conte como tentativa.
	Cancelado
)

func (c Classe) String() string {
	switch c {
	case Ok:
		return "ok"
	case Passageiro:
		return "passageiro"
	case Definitivo:
		return "definitivo"
	case Configuracao:
		return "configuração"
	case Cancelado:
		return "cancelado"
	}
	return fmt.Sprintf("Classe(%d)", int(c))
}

// Classificar reduz qualquer erro de um [Motor] ou [Conversor] a uma [Classe].
func Classificar(err error) Classe {
	switch {
	case err == nil:
		return Ok
	// Antes de tudo: um Ctrl+C no meio não pode marcar o áudio como perdido.
	case errors.Is(err, context.Canceled):
		return Cancelado
	case errors.Is(err, ErrMotorAusente), errors.Is(err, ErrCredencial):
		return Configuracao
	case errors.Is(err, ErrAudioInvalido):
		return Definitivo
	}
	// Na dúvida, passageiro: quem repete tem limite de tentativas; quem desiste
	// cedo perde o áudio.
	return Passageiro
}
