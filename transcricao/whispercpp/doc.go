/*
Package whispercpp transcreve áudio localmente com o whisper.cpp, chamando a
whisper-cli como processo externo — um processo por áudio, sem CGO.

Nada sai da máquina. O preço é instalar dois programas e baixar um modelo:

	scoop install whisper-cpp ffmpeg     (Windows)
	brew install whisper-cpp ffmpeg      (macOS)

e um arquivo ggml de https://huggingface.co/ggerganov/whisper.cpp — por
exemplo ggml-large-v3-turbo-q5_0.bin ou ggml-small-q5_1.bin.

Nota de voz do WhatsApp é Opus, que a whisper-cli não lê: configure um
[transcricao.Conversor], normalmente o de transcricao/ffmpeg.

O que o pacote cuida por quem chama:

  - idioma: sem [transcricao.Pedido].Idioma vai "auto", porque o padrão da
    whisper-cli é inglês, não detectar;
  - prova de sucesso: a whisper-cli sai com 0 quando pula um arquivo que não
    conseguiu ler, então sucesso é o JSON escrito, não o código de saída;
  - prazo: o processo é encerrado quando passa de [Config].Limite e quando o
    contexto é cancelado;
  - sobras: cada transcrição trabalha numa pasta própria dentro de
    [Config].DirTemp, apagada no fim, com ou sem erro;
  - classificação: modelo que não carrega e whisper-cli sem uma opção são
    [transcricao.ErrMotorAusente]; áudio ilegível é
    [transcricao.ErrAudioInvalido].

Estabilidade: v0.

In English: package whispercpp is a local speech-to-text engine that runs the
whisper.cpp command-line tool (whisper-cli) as an external process, one per
file, with timeouts, cancellation, cleanup and error classification.
*/
package whispercpp
