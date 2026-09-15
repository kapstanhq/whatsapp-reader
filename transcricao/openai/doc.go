/*
Package openai transcreve áudio por uma API compatível com a de transcrição da
OpenAI (POST /audio/transcriptions, multipart): a própria OpenAI, a Groq, um
Speaches na rede local.

Os áudios SAEM da máquina. Numa conversa de WhatsApp eles são a voz de outras
pessoas; mandar para um serviço de terceiros é decisão de quem configura, com
as obrigações que vêm junto (LGPD, no Brasil). Por isso o pacote não lê chave
do ambiente: a [Config] recebe tudo explicitamente.

O que o pacote cuida por quem chama:

  - o arquivo vai como "audio.<ext>": o nome no disco não sai, e a extensão
    fica porque é por ela que a API reconhece o formato;
  - arquivo maior que [Config].TamanhoMax nem é enviado;
  - a chave não aparece em mensagem de erro, nem quando o serviço a repete;
  - os erros viram os sentinelas de transcricao: 401, 403 e conta sem crédito
    são [transcricao.ErrCredencial]; 404 e modelo inexistente,
    [transcricao.ErrMotorAusente]; 400, 413, 415 e 422,
    [transcricao.ErrAudioInvalido]; 429 é [transcricao.ErroLimite] com o
    Retry-After; 5xx e falha de rede ficam passageiros.

Estabilidade: v0.

In English: package openai is a speech-to-text engine for any OpenAI-compatible
transcription API (OpenAI, Groq, Speaches). It never reads API keys from the
environment, never includes the key in errors, and maps HTTP failures to the
transcricao error classes. Audio leaves the machine.
*/
package openai
