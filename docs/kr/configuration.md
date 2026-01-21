# 설정 레퍼런스

kar98k는 YAML 설정 파일을 사용합니다. 이 문서에서는 사용 가능한 모든 옵션을 설명합니다.

## 전체 설정 예시

```yaml
targets:
  - name: api-health
    url: http://localhost:8080/api/health
    protocol: http
    method: GET
    weight: 50
    timeout: 10s

  - name: api-users
    url: http://localhost:8080/api/users
    protocol: http
    method: GET
    headers:
      Authorization: "Bearer ${API_TOKEN}"
      Content-Type: "application/json"
    weight: 30
    timeout: 15s

  - name: api-data
    url: http://localhost:8080/api/data
    protocol: http
    method: POST
    headers:
      Content-Type: "application/json"
    body: '{"query": "test"}'
    weight: 20
    timeout: 20s

controller:
  base_tps: 100
  max_tps: 1000
  ramp_up_duration: 30s
  shutdown_timeout: 30s
  schedule:
    - hours: [9, 10, 11, 12, 13, 14, 15, 16, 17]
      tps_multiplier: 1.5
    - hours: [12, 13]
      tps_multiplier: 2.0
    - hours: [0, 1, 2, 3, 4, 5]
      tps_multiplier: 0.3

pattern:
  poisson:
    enabled: true
    lambda: 0.1
    spike_factor: 3.0
    min_interval: 30s
    max_interval: 5m
    ramp_up: 5s
    ramp_down: 10s
  noise:
    enabled: true
    amplitude: 0.15

worker:
  pool_size: 1000
  queue_size: 10000
  max_idle_conns: 100
  idle_conn_timeout: 90s

health:
  enabled: true
  interval: 10s
  timeout: 5s

metrics:
  enabled: true
  address: ":9090"
  path: "/metrics"
```

## 설정 섹션

### targets

트래픽을 보낼 대상 엔드포인트 목록입니다.

| 필드 | 타입 | 필수 | 기본값 | 설명 |
|------|------|------|--------|------|
| `name` | string | 예 | - | 대상의 고유 식별자 |
| `url` | string | 예 | - | 프로토콜과 경로를 포함한 전체 URL |
| `protocol` | string | 아니오 | `http` | 프로토콜: `http`, `http2`, 또는 `grpc` |
| `method` | string | 아니오 | `GET` | HTTP 메서드 |
| `headers` | map | 아니오 | - | 요청 헤더 |
| `body` | string | 아니오 | - | 요청 본문 |
| `weight` | int | 아니오 | `100` | 부하 분배를 위한 상대적 가중치 |
| `timeout` | duration | 아니오 | `30s` | 요청 타임아웃 |

### controller

메인 트래픽 생성 동작을 제어합니다.

| 필드 | 타입 | 필수 | 기본값 | 설명 |
|------|------|------|--------|------|
| `base_tps` | float | 아니오 | `100` | 기본 초당 트랜잭션 수 |
| `max_tps` | float | 아니오 | `1000` | 최대 TPS 상한 |
| `ramp_up_duration` | duration | 아니오 | `30s` | 시작 시 기본 TPS에 도달하는 시간 |
| `shutdown_timeout` | duration | 아니오 | `30s` | 우아한 종료를 기다리는 최대 시간 |
| `schedule` | list | 아니오 | - | 시간대별 TPS 배율 |

#### schedule

| 필드 | 타입 | 설명 |
|------|------|------|
| `hours` | list[int] | 이 배율이 적용되는 시간 (0-23) |
| `tps_multiplier` | float | 기본 TPS에 적용할 배율 |

### pattern

트래픽 패턴 생성을 제어합니다.

#### pattern.poisson

무작위 트래픽 스파이크를 위한 포아송 분포입니다.

| 필드 | 타입 | 필수 | 기본값 | 설명 |
|------|------|------|--------|------|
| `enabled` | bool | 아니오 | `true` | 포아송 스파이크 활성화 |
| `lambda` | float | 아니오 | `0.1` | 초당 평균 스파이크 수 |
| `spike_factor` | float | 아니오 | `3.0` | 스파이크 시 TPS 배율 |
| `min_interval` | duration | 아니오 | `30s` | 스파이크 간 최소 시간 |
| `max_interval` | duration | 아니오 | `5m` | 스파이크 간 최대 시간 |
| `ramp_up` | duration | 아니오 | `5s` | 피크 스파이크에 도달하는 시간 |
| `ramp_down` | duration | 아니오 | `10s` | 기본 상태로 돌아오는 시간 |

#### pattern.noise

현실적인 트래픽을 위한 마이크로 변동입니다.

| 필드 | 타입 | 필수 | 기본값 | 설명 |
|------|------|------|--------|------|
| `enabled` | bool | 아니오 | `true` | 노이즈 활성화 |
| `amplitude` | float | 아니오 | `0.15` | 변동 범위 (0.15 = ±15%) |

### worker

워커 풀 설정입니다.

| 필드 | 타입 | 필수 | 기본값 | 설명 |
|------|------|------|--------|------|
| `pool_size` | int | 아니오 | `1000` | 최대 동시 워커 수 |
| `queue_size` | int | 아니오 | `10000` | 요청 큐 크기 |
| `max_idle_conns` | int | 아니오 | `100` | HTTP keep-alive 연결 수 |
| `idle_conn_timeout` | duration | 아니오 | `90s` | 연결 유휴 타임아웃 |

### health

헬스 체커 설정입니다.

| 필드 | 타입 | 필수 | 기본값 | 설명 |
|------|------|------|--------|------|
| `enabled` | bool | 아니오 | `true` | 헬스 체크 활성화 |
| `interval` | duration | 아니오 | `10s` | 헬스 체크 간격 |
| `timeout` | duration | 아니오 | `5s` | 헬스 체크 타임아웃 |

### metrics

Prometheus 메트릭 설정입니다.

| 필드 | 타입 | 필수 | 기본값 | 설명 |
|------|------|------|--------|------|
| `enabled` | bool | 아니오 | `true` | 메트릭 엔드포인트 활성화 |
| `address` | string | 아니오 | `:9090` | 리슨 주소 |
| `path` | string | 아니오 | `/metrics` | 메트릭 엔드포인트 경로 |

## 환경 변수

설정에서 환경 변수를 사용할 수 있습니다:

```yaml
headers:
  Authorization: "Bearer ${API_TOKEN}"
```

참고: 환경 변수 치환은 배포 시스템(예: Docker Compose, Kubernetes)에서 처리해야 합니다.

## 설정 검증

kar98k는 시작 시 설정을 검증합니다:

- 최소 하나의 타겟이 필요합니다
- `base_tps`는 양수여야 합니다
- `max_tps`는 `base_tps` 이상이어야 합니다
- 활성화된 경우 `poisson.lambda`는 양수여야 합니다
- `poisson.spike_factor`는 1 이상이어야 합니다
- `noise.amplitude`는 0과 1 사이여야 합니다
- `worker.pool_size`는 양수여야 합니다
