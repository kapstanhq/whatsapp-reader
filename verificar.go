package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
	"github.com/kapstanhq/whatsapp-reader/transcricao/ffmpeg"
)

/* `whatsapp-reader verificar [arquivo]`: o que falta para transcrever, sem
   precisar do daemon no ar nem de alguém mandar um áudio.

   Existe porque a transcrição depende de coisas instaladas à parte, e a falta
   de qualquer uma só apareceria como fila parada no estado da ponte. Com um
   arquivo, transcreve e mede — é assim que se escolhe o modelo: o preferido se
   a máquina transcreve mais rápido que a duração do áudio; o leve, se não. */

type peca struct {
	nome, onde, dica string
	ok               bool
}

func Verificar(dir string, args []string) error {
	mt := montarMotor(dir, os.Getenv)
	fmt.Println("transcrição:", mt.descricao)
	if mt.modo == modoDesligada {
		if mt.problema != "" {
			return errors.New(mt.problema)
		}
		return nil
	}

	fmt.Println()
	faltam := 0
	for _, p := range pecasDoMotor(mt, exec.LookPath, runtime.GOOS) {
		if p.ok {
			fmt.Printf("  ok     %-12s %s\n", p.nome, p.onde)
			continue
		}
		faltam++
		fmt.Printf("  FALTA  %-12s %s\n", p.nome, p.onde)
		for _, linha := range strings.Split(p.dica, "\n") {
			fmt.Printf("         %-12s %s\n", "", linha)
		}
	}
	if mt.modo == modoAPI {
		fmt.Printf("\n!! neste modo os áudios SAEM desta máquina e vão para %s.\n", hostDaAPI(mt.url))
		fmt.Println("   São a voz de outras pessoas: a decisão, e o que a LGPD pede dela, é de quem configura.")
	}
	switch {
	case faltam == 1:
		return errors.New("falta 1 peça para transcrever")
	case faltam > 1:
		return fmt.Errorf("faltam %d peças para transcrever", faltam)
	case len(args) == 0:
		fmt.Println("\ntudo pronto. Para medir a velocidade: whatsapp-reader verificar <nota.ogg>")
		return nil
	}
	return transcreverParaVer(dir, mt, args[0])
}

func pecasDoMotor(mt motorMontado, procurar func(string) (string, error), goos string) []peca {
	instalar := func(windows, mac, outros string) string {
		switch goos {
		case "windows":
			return windows
		case "darwin":
			return mac
		}
		return outros
	}
	executavel := func(nome, valor, variavel, dica string) peca {
		p := peca{nome: nome}
		if caminho, err := procurar(valor); err == nil {
			p.ok, p.onde = true, caminho
			return p
		}
		p.onde = valor + " não encontrado"
		p.dica = dica + "\nou aponte " + variavel + " para o executável"
		return p
	}

	switch mt.modo {
	case modoLocal:
		modelo := peca{nome: "modelo", onde: mt.modelo}
		if st, err := os.Stat(mt.modelo); err == nil && !st.IsDir() {
			modelo.ok, modelo.onde = true, fmt.Sprintf("%s (%s)", mt.modelo, tamanhoHumano(st.Size()))
		} else {
			modelo.onde += " não existe"
			base := filepath.Base(mt.modelo)
			modelo.dica = "baixe https://huggingface.co/ggerganov/whisper.cpp/resolve/main/" + base +
				"\ne salve como " + mt.modelo
			if base == modelosConhecidos[0] {
				modelo.dica += "\n(ou " + modelosConhecidos[1] + ", mais leve, na mesma pasta, se a máquina for lenta)"
			}
			if !slices.Contains(modelosConhecidos, base) && !strings.HasPrefix(base, "ggml-") {
				modelo.dica = "o arquivo apontado por WHATSAPP_READER_WHISPER_MODELO não existe"
			}
		}
		return []peca{
			executavel("whisper-cli", mt.whisper, "WHATSAPP_READER_WHISPER", instalar(
				"instale: scoop install whisper-cpp",
				"instale: brew install whisper-cpp",
				"compile de https://github.com/ggml-org/whisper.cpp ou use o pacote da sua distribuição")),
			executavel("ffmpeg", mt.ffmpeg, "WHATSAPP_READER_FFMPEG", instalar(
				"instale: scoop install ffmpeg",
				"instale: brew install ffmpeg",
				"instale: sudo apt install ffmpeg (ou o gerenciador da sua distribuição)")),
			modelo,
		}

	case modoAPI:
		endereco := peca{nome: "endereço", onde: mt.url, ok: true}
		if u, err := url.Parse(mt.url); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			endereco.ok, endereco.onde = false, "WHATSAPP_READER_API_URL vazia ou sem http(s)://"
			endereco.dica = "defina WHATSAPP_READER_API_URL: https://api.groq.com/openai/v1,\nhttps://api.openai.com/v1 ou o endereço do seu servidor"
		}
		modelo := peca{nome: "modelo", onde: mt.modeloAPI, ok: mt.modeloAPI != ""}
		if !modelo.ok {
			modelo.onde = "WHATSAPP_READER_API_MODELO vazia"
			modelo.dica = "defina WHATSAPP_READER_API_MODELO: whisper-large-v3-turbo na Groq,\nwhisper-1 ou gpt-4o-mini-transcribe na OpenAI"
		}
		chave := peca{nome: "chave", onde: "definida (não é mostrada)", ok: mt.temChave}
		if !mt.temChave {
			if servidorLocal(mt.url) {
				chave.ok, chave.onde = true, "sem chave: servidor nesta máquina"
			} else {
				chave.onde, chave.dica = "WHATSAPP_READER_API_CHAVE vazia", "defina WHATSAPP_READER_API_CHAVE com a chave do serviço"
			}
		}
		return []peca{endereco, modelo, chave}
	}
	return nil
}

func servidorLocal(endereco string) bool {
	u, err := url.Parse(endereco)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func transcreverParaVer(dir string, mt motorMontado, arquivo string) error {
	abs, err := filepath.Abs(arquivo)
	if err == nil {
		_, err = os.Stat(abs)
	}
	if err != nil {
		return err
	}
	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt)
	defer parar()

	duracao := duracaoDoAudio(ctx, mt.ffmpeg, abs)
	fmt.Printf("\ntranscrevendo %s…\n", filepath.Base(abs))
	r, err := mt.motor.Transcrever(ctx, transcricao.Audio{Caminho: abs, Duracao: duracao},
		transcricao.Pedido{Idioma: mt.idioma, Dica: lerVocabulario(dir)})
	if err != nil {
		return fmt.Errorf("a transcrição falhou (%s): %w", transcricao.Classificar(err), err)
	}
	fmt.Println(ritmo(duracao, r.Levou), "·", r.Modelo)
	texto := r.Texto
	if texto == "" {
		texto = "(sem fala reconhecível)"
	}
	fmt.Println("\n" + texto)
	return nil
}

// A duração sai do WAV que o ffmpeg gera: um arquivo solto não traz a que o
// WhatsApp declara, e sem ela não dá para dizer se a máquina acompanha o modelo.
func duracaoDoAudio(ctx context.Context, executavel, caminho string) time.Duration {
	tmp, err := os.MkdirTemp("", "whatsapp-reader-verificar-")
	if err != nil {
		return 0
	}
	defer os.RemoveAll(tmp)
	wav := filepath.Join(tmp, "medida.wav")
	if ffmpeg.Novo(ffmpeg.Config{Executavel: executavel}).ParaWAV(ctx, caminho, wav) != nil {
		return 0
	}
	st, err := os.Stat(wav)
	if err != nil {
		return 0
	}
	// PCM de 16 kHz, mono, 16 bits: 32 000 bytes por segundo depois do cabeçalho.
	return time.Duration(float64(st.Size()-44) / 32000 * float64(time.Second))
}

// "0:42 de áudio em 0:31 (0,7× a duração)": abaixo de 1× a fila anda; acima, acumula.
func ritmo(audio, levou time.Duration) string {
	if audio <= 0 {
		return "transcrito em " + minutos(levou)
	}
	fator := strings.Replace(fmt.Sprintf("%.1f", levou.Seconds()/audio.Seconds()), ".", ",", 1)
	return fmt.Sprintf("%s de áudio em %s (%s× a duração)", minutos(audio), minutos(levou), fator)
}

func minutos(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
