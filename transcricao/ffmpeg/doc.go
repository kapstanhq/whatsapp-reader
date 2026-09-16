/*
Package ffmpeg converte áudio para o WAV PCM de 16 kHz mono que os motores
locais de transcrição leem, chamando o executável ffmpeg.

Nota de voz do WhatsApp é Opus dentro de Ogg, e a whisper-cli, na compilação
padrão, não lê Opus. Converter é trabalho do ffmpeg — instalado à parte
(scoop install ffmpeg, brew install ffmpeg, apt install ffmpeg), nunca
embutido nem linkado.

[Conversor] implementa [transcricao.Conversor] e [transcricao.Verificador]. Os
erros embrulham os sentinelas de transcricao: executável ausente é
[transcricao.ErrMotorAusente]; áudio que o ffmpeg não lê, ou que sai sem
nenhuma amostra, é [transcricao.ErrAudioInvalido]. Origem inexistente não é
áudio inválido — o arquivo pode ser baixado de novo — e vem com
[io/fs.ErrNotExist].

Estabilidade: v0.

In English: package ffmpeg converts audio files to 16 kHz mono PCM WAV by
running an external ffmpeg binary.
*/
package ffmpeg
