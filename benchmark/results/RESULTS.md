# Benchmark: ControlFinan em Rails x Django x Laravel x Go

Mesmo app (paridade de telas e regras), mesmos dados, modo produção.
Puma e Gunicorn: 2 processos × 5 threads (com GVL/GIL, até 2 requisições usam CPU ao mesmo tempo); Laravel Octane: 2 workers, um por CPU; Go: goroutines em 2 threads (GOMAXPROCS = limite de CPU).
Cada app limitado a 2 CPUs e 1 GB de RAM, com o seu Postgres 16 (2 CPUs, 1 GB).
Carga: k6 com 20 usuários simultâneos, 10 s de aquecimento + 30 s medidos por tela, logado de verdade.
Os dados são carregados e analisados (`ANALYZE`) antes da medição.

Versões: Rails 8.1 + Hotwire (Puma); Django 5.2 (Gunicorn); Laravel 12 + Octane (FrankenPHP); Go 1.25 (net/http + pgx).

## Requisições por segundo e latência

| Tela | App | req/s | p50 (ms) | p95 (ms) | p99 (ms) | erros | CPU média | memória pico | CPU do banco |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Conta Corrente (30 itens + saldo) | Rails | 93 | 216.6 | 283.2 | 304.7 | 0.0% | 192% | 246 MiB | 46% |
| Conta Corrente (30 itens + saldo) | Django | 78 | 256.5 | 437.2 | 598.8 | 0.0% | 195% | 104 MiB | 41% |
| Conta Corrente (30 itens + saldo) | Laravel | 119 | 166.7 | 200.2 | 229.6 | 0.0% | 151% | 101 MiB | 48% |
| Conta Corrente (30 itens + saldo) | Go | 119 | 188.3 | 298.3 | 363.0 | 0.0% | 92% | 11 MiB | 196% |
| Saldo do dia (filtro) | Rails | 247 | 79.8 | 102.8 | 112.4 | 0.0% | 192% | 253 MiB | 18% |
| Saldo do dia (filtro) | Django | 154 | 111.1 | 324.5 | 444.2 | 0.0% | 199% | 106 MiB | 25% |
| Saldo do dia (filtro) | Laravel | 219 | 89.6 | 110.2 | 132.6 | 0.0% | 173% | 102 MiB | 27% |
| Saldo do dia (filtro) | Go | 2484 | 7.8 | 11.3 | 13.1 | 0.0% | 201% | 10 MiB | 105% |
| Dashboard do mês | Rails | 163 | 117.4 | 185.3 | 203.2 | 0.0% | 194% | 259 MiB | 29% |
| Dashboard do mês | Django | 143 | 120.4 | 265.5 | 394.0 | 0.0% | 185% | 106 MiB | 36% |
| Dashboard do mês | Laravel | 222 | 89.2 | 102.4 | 117.8 | 0.0% | 156% | 102 MiB | 43% |
| Dashboard do mês | Go | 2042 | 9.5 | 13.1 | 15.9 | 0.0% | 187% | 10 MiB | 192% |
| API JSON (30 itens) | Rails | 304 | 67.0 | 91.1 | 100.6 | 0.0% | 192% | 284 MiB | 155% |
| API JSON (30 itens) | Django | 225 | 77.9 | 174.2 | 281.4 | 0.0% | 179% | 107 MiB | 24% |
| API JSON (30 itens) | Laravel | 213 | 92.8 | 107.5 | 115.0 | 0.0% | 184% | 102 MiB | 19% |
| API JSON (30 itens) | Go | 4867 | 3.5 | 6.8 | 11.9 | 0.0% | 176% | 10 MiB | 196% |
| Criar lançamento (POST) | Rails | 409 | 47.9 | 55.2 | 60.4 | 0.0% | 204% | 311 MiB | 32% |
| Criar lançamento (POST) | Django | 447 | 42.1 | 59.6 | 143.4 | 0.0% | 196% | 107 MiB | 41% |
| Criar lançamento (POST) | Laravel | 305 | 63.9 | 78.2 | 98.2 | 0.0% | 184% | 103 MiB | 28% |
| Criar lançamento (POST) | Go | 5691 | 2.4 | 7.7 | 23.3 | 0.0% | 145% | 10 MiB | 206% |

## Quem foi mais rápido (req/s)

| Tela | Mais rápido | Ordem |
|---|---|---|
| Conta Corrente (30 itens + saldo) | Go (1.0x Laravel, 1.3x Rails, 1.5x Django) | Go > Laravel > Rails > Django |
| Saldo do dia (filtro) | Go (10.1x Rails, 11.3x Laravel, 16.1x Django) | Go > Rails > Laravel > Django |
| Dashboard do mês | Go (9.2x Laravel, 12.5x Rails, 14.3x Django) | Go > Laravel > Rails > Django |
| API JSON (30 itens) | Go (16.0x Rails, 21.7x Django, 22.8x Laravel) | Go > Rails > Django > Laravel |
| Criar lançamento (POST) | Go (12.7x Django, 13.9x Rails, 18.6x Laravel) | Go > Django > Rails > Laravel |

Vitórias: Rails 0, Django 0, Laravel 0, Go 5.

## Boot, memória e imagem

| | Rails | Django | Laravel | Go |
|---|---:|---:|---:|---:|
| Subir até responder `/up` (inclui migrations) | 5.3 s | 3.0 s | 2.2 s | 0.9 s |
| Memória parado | 124 MiB | 83 MiB | 81 MiB | 3 MiB |
| Tamanho da imagem Docker | 519 MB | 168 MB | 224 MB | 13 MB |

## Código

| | Rails | Django | Laravel | Go |
|---|---:|---:|---:|---:|
| Linhas de código do app (sem linhas em branco) | 787 | 1039 | 1116 | 1658 |
| Linhas de teste | 549 | 392 | 338 | 579 |
