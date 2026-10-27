# ControlFinan (Go)

![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)
![Tailwind](https://img.shields.io/badge/Tailwind-4-06B6D4?logo=tailwindcss&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white)

Aplicação web de controle financeiro pessoal, pensada para quem trabalha como motorista de aplicativo. Cada usuário registra entradas (créditos) e saídas (débitos) em contas como dinheiro, bancos e carteiras de aplicativos, e acompanha o saldo por dia e por mês.

Esta é a versão em **Go** do [ControlFinan em Rails 8 + Hotwire](https://github.com/igorcb/controlfinan-rails8-hotwire). Ela tem **paridade de funcionalidades e de visual** com o original, assim como as versões em [Django](https://github.com/igorcb/controlfinan-django) e em [Laravel](https://github.com/igorcb/controlfinan-laravel): mesmas telas, mesmas regras, mesmas mensagens e **mesmo esquema de banco**. As versões podem usar os mesmos dados, inclusive as senhas.

Usa só a biblioteca padrão do Go (`net/http` e `html/template`), mais o driver [pgx](https://github.com/jackc/pgx) e o `bcrypt` de `golang.org/x/crypto`.

## Funcionalidades

| Tela | Rota | O que faz |
|---|---|---|
| Conta Corrente | `/` | Lista os lançamentos, do mais recente para o mais antigo, 30 por página, com o saldo total |
| Saldo do dia | `/current_accounts?date=AAAA-MM-DD` | Filtra a listagem por um dia e mostra o saldo daquele dia |
| Novo lançamento | `/current_accounts/new` | Cadastra um crédito ou um débito |
| Detalhe, edição e exclusão | `/current_accounts/:id` | Mostra, altera ou remove um lançamento |
| Dashboard | `/dashboard` | Abas **Saldo Diário** e **Saldo Mensal** (corridas menos combustível, lavagem e telefone). As setas navegam entre os meses |
| JSON | `/current_accounts.json` e `/current_accounts/:id.json` | Os mesmos dados, para integrações |
| Cadastro, login e senha | `/registration/new`, `/session/new`, `/passwords/new` | Conta de usuário, com senha de no mínimo 6 caracteres |

Cada usuário só enxerga os próprios lançamentos.

## Como a paridade foi feita

| Rails 8 + Hotwire | Go |
|---|---|
| Autenticação nativa do Rails 8 (`has_secure_password`, tabela `sessions`, cookie `session_id`) | Mesmo modelo: tabela `sessions` e cookie assinado `session_id`. O `bcrypt` do Go grava no mesmo formato do Rails (`$2a$`) |
| Proteção CSRF (`authenticity_token`) | Token em cookie repetido em cada formulário, com o mesmo nome de campo |
| `rate_limit to: 10, within: 3.minutes` | Limite de 10 tentativas a cada 3 minutos por IP, em memória |
| Token de redefinição de senha (15 minutos) | Token assinado com validade de 15 minutos, invalidado quando a senha muda |
| `_method` para PATCH, PUT e DELETE | O mesmo campo `_method` nos formulários |
| Turbo (`data-turbo-confirm`) e Stimulus (abas) | JavaScript simples para a confirmação e as abas |
| jbuilder | JSON compacto com os mesmos campos e o mesmo formato (por exemplo, `"price":"150.0"`) |
| Tailwind CSS 4 | Tailwind CSS 4 (CLI standalone), com as mesmas classes |

Os valores em dinheiro ficam em centavos (`int64`), sem ponto flutuante.

**Verificação**, com o mesmo usuário e os mesmos dados (7.292 lançamentos) nos dois apps:

- Texto idêntico em todas as telas comparadas, incluindo paginação, filtro por dia, datas e meses inválidos, dashboard, formulários, mensagens de validação, erros de cadastro e login inválido.
- JSON idêntico byte a byte (lista, segunda página e detalhe).
- **14 telas idênticas pixel a pixel**: login, lista, filtro, dashboard, novo, detalhe e edição, no computador (1280 px) e no celular (390 px).
- Roteiro completo no navegador (Playwright): cadastro, login, lançamentos, edição, exclusão, dashboard, recuperação de senha e celular.

## Benchmark: Rails x Django x Laravel x Go

Mesmo app, mesmos dados (7.292 lançamentos) e todos em **modo produção**:

- **Rails 8.1** com Puma (Ruby 3.4 + YJIT) e **Django 5.2** com Gunicorn: 2 processos × 5 threads cada.
- **Laravel 12** com Octane + FrankenPHP: 2 workers, um por CPU.
- **Go 1.25** com `net/http`: goroutines em 2 threads (o Go lê o limite de CPU do container) e até 10 conexões com o banco.
- Cada app limitado a **2 CPUs e 1 GB de RAM**, cada um com o seu Postgres 16 (2 CPUs, 1 GB).
- Carga com **k6**: 20 usuários simultâneos, logados de verdade pelo formulário de login, 10 s de aquecimento e 30 s medidos por tela. Os dados são carregados e analisados (`ANALYZE`) antes da medição.

### Requisições por segundo (mais é melhor)

| Tela | Rails | Django | Laravel | Go |
|---|---:|---:|---:|---:|
| Conta Corrente (30 itens + saldo) | 93 | 78 | 119 | **119** |
| Saldo do dia (filtro) | 247 | 154 | 219 | **2.484** |
| Dashboard do mês | 163 | 143 | 222 | **2.042** |
| API JSON (30 itens) | 304 | 225 | 213 | **4.867** |
| Criar lançamento (POST) | 409 | 447 | 305 | **5.691** |

Nenhuma requisição falhou. Latências, CPU do app, CPU do banco e memória de cada tela estão em [benchmark/results/RESULTS.md](benchmark/results/RESULTS.md).

### Recursos

| | Rails | Django | Laravel | Go |
|---|---:|---:|---:|---:|
| Subir até responder `/up` (inclui migrations) | 5,3 s | 3,0 s | 2,2 s | **0,9 s** |
| Memória parado | 124 MiB | 83 MiB | 81 MiB | **3 MiB** |
| Memória no pico da carga | ~310 MiB | ~107 MiB | ~103 MiB | **11 MiB** |
| Tamanho da imagem Docker | 519 MB | 168 MB | 224 MB | **13 MB** |

### O que os números mostram

- **Go venceu as 5 telas.** Nas telas leves, fez de 9 a 23 vezes mais requisições por segundo, com latência mediana de 2 a 10 ms, contra 40 a 260 ms nos outros. Usou de 10 a 30 vezes menos memória.
- **Na lista, Go e Laravel empataram (119 req/s)** e todos ficaram perto. Ela calcula o saldo total somando os ~7.300 lançamentos em `numeric` a cada requisição, e quem limita é o Postgres: no Go, o banco ficou a 196% de CPU (o limite é 200%) com o app a 92%. A linguagem só aparece quando o banco não é o gargalo.
- Entre os outros três, cada um venceu algo: Rails no filtro e no JSON, Django no POST, Laravel na lista e no dashboard.
- O preço do Go é código: são 1.658 linhas, contra 787 no Rails. Sessão, CSRF, flash, limite de tentativas e validações foram escritos à mão, onde o Rails já traz tudo pronto.

### Lições de medição

- **Estatísticas do banco.** Na primeira rodada, a lista do Go deu 49 req/s porque era medida logo depois da carga dos dados, antes de o Postgres atualizar as estatísticas da tabela. Agora o script roda `ANALYZE` antes de medir, para todos os apps.
- Nas versões em [Laravel](https://github.com/igorcb/controlfinan-laravel#lições-de-medição) e [Django](https://github.com/igorcb/controlfinan-django) estão as outras lições: conexão com o banco a cada requisição, workers demais para a CPU e um token CSRF errado no script de teste. **Meça, desconfie e confira.**

### Como reproduzir

```bash
# Requisitos: ../controlfinan-rails8-hotwire (com o banco de desenvolvimento no ar, que fornece os dados),
# ../controlfinan-django e ../controlfinan-laravel ao lado deste repositório.
./benchmark/run.sh        # os quatro: rails django laravel go
./benchmark/run.sh go     # só alguns; os resultados dos outros são mantidos
```

## Como rodar com Docker

```bash
docker compose up               # app em http://localhost:8003
```

O container de desenvolvimento compila o Tailwind e roda o app com `go run .`. As tabelas são criadas na subida (mesmo esquema do app Rails). Depois de mudar código Go, reinicie com `docker compose restart web`.

Imagem de produção: `Dockerfile.prod`, um binário estático (templates, CSS, JS e fuso horário embutidos) numa imagem vazia, com cerca de 13 MB. Configure `DATABASE_URL`, `SECRET_KEY_BASE` e, se quiser, `BCRYPT_COST` (padrão 12), `DB_POOL` (padrão 10) e `PORT` (padrão 8000).

## Testes

36 testes com o pacote `testing` do Go, usando Postgres de verdade, cobrindo:

- Formatação de moeda e decimais.
- Senha no formato do Rails, incluindo um hash gravado pelo próprio Rails.
- Validações em pt-BR, saldo e resumo do mês.
- Login, limite de tentativas, logout, CSRF, cadastro e redefinição de senha.
- Lançamentos: filtro, paginação, JSON, criar, editar, excluir e isolamento entre usuários.
- Dashboard.

```bash
docker compose exec web go test ./...
```

Os testes usam o banco `controlfinan_go_test`, criado automaticamente.

## Estrutura

```
main.go                 configuração (variáveis de ambiente) e servidor HTTP
app/server.go           rotas e handlers
app/web.go              cookies assinados, sessão, CSRF, flash e limite de tentativas
app/models.go           usuários, sessões, lançamentos, validações e consultas
app/format.go           moeda e decimais como no Rails
app/templates/          HTML com o mesmo resultado do app Rails
app/static/             JavaScript e ícones (o CSS é gerado pelo Tailwind)
assets/                 fonte do Tailwind
benchmark/              comparação com Rails, Django e Laravel (k6)
```

## Licença

[MIT](LICENSE)
