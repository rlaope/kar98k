# API 레퍼런스

## 커맨드 라인 인터페이스

### 사용법

```bash
kar98k [플래그]
```

### 플래그

| 플래그 | 타입 | 기본값 | 설명 |
|--------|------|--------|------|
| `-config` | string | `configs/kar98k.yaml` | 설정 파일 경로 |
| `-version` | bool | `false` | 버전 표시 후 종료 |

### 예시

```bash
# 기본 설정으로 실행
./kar98k

# 사용자 정의 설정으로 실행
./kar98k -config /path/to/config.yaml

# 버전 표시
./kar98k -version
```

## HTTP 엔드포인트

kar98k는 메트릭 주소(기본값 `:9090`)에서 여러 HTTP 엔드포인트를 노출합니다.

### GET /metrics

Prometheus 메트릭 엔드포인트입니다.

**응답:** Prometheus 텍스트 형식

```
# HELP kar98k_requests_total Total number of requests by target and status
# TYPE kar98k_requests_total counter
kar98k_requests_total{protocol="http",status="success",target="api-health"} 12345

# HELP kar98k_request_duration_seconds Request latency histogram
# TYPE kar98k_request_duration_seconds histogram
kar98k_request_duration_seconds_bucket{protocol="http",target="api-health",le="0.001"} 100
...
```

### GET /healthz

Liveness 프로브 엔드포인트입니다.

**응답:**
- 상태: `200 OK`
- 본문: `ok`

### GET /readyz

Readiness 프로브 엔드포인트입니다.

**응답:**
- 상태: `200 OK`
- 본문: `ok`

## Prometheus 메트릭

### 카운터

#### kar98k_requests_total

전송된 총 요청 수입니다.

**레이블:**
| 레이블 | 설명 |
|--------|------|
| `target` | 대상 이름 |
| `status` | `success` 또는 `error` |
| `protocol` | `http`, `http2`, 또는 `grpc` |

**예시 쿼리:**
```promql
# 대상별 요청 속도
rate(kar98k_requests_total[5m])

# 에러 비율
sum(rate(kar98k_requests_total{status="error"}[5m])) /
sum(rate(kar98k_requests_total[5m]))
```

### 히스토그램

#### kar98k_request_duration_seconds

요청 지연 분포입니다.

**레이블:**
| 레이블 | 설명 |
|--------|------|
| `target` | 대상 이름 |
| `protocol` | 사용된 프로토콜 |

**버킷:** 1ms에서 ~16s까지 지수

**예시 쿼리:**
```promql
# 95번째 백분위 지연
histogram_quantile(0.95, rate(kar98k_request_duration_seconds_bucket[5m]))

# 평균 지연
rate(kar98k_request_duration_seconds_sum[5m]) /
rate(kar98k_request_duration_seconds_count[5m])
```

### 게이지

#### kar98k_requests_in_flight

현재 처리 중인 요청 수입니다.

**예시 쿼리:**
```promql
kar98k_requests_in_flight
```

#### kar98k_current_tps

생성 중인 실제 TPS입니다 (지난 1초 동안 측정).

**예시 쿼리:**
```promql
kar98k_current_tps
```

#### kar98k_target_tps

패턴 엔진의 목표 TPS 설정입니다.

**예시 쿼리:**
```promql
# TPS 정확도
kar98k_current_tps / kar98k_target_tps
```

#### kar98k_active_workers

활성 워커 고루틴 수입니다.

**예시 쿼리:**
```promql
kar98k_active_workers
```

#### kar98k_queued_requests

큐에서 대기 중인 요청 수입니다.

**예시 쿼리:**
```promql
# 큐 사용률 (큐 크기 10000 가정)
kar98k_queued_requests / 10000
```

#### kar98k_spike_active

트래픽 스파이크가 현재 활성 상태인지 여부입니다.

**값:**
- `1` - 스파이크 활성
- `0` - 스파이크 없음

**예시 쿼리:**
```promql
# 스파이크 지속 시간
changes(kar98k_spike_active[1h])
```

#### kar98k_target_health

각 대상의 헬스 상태입니다.

**레이블:**
| 레이블 | 설명 |
|--------|------|
| `target` | 대상 이름 |

**값:**
- `1` - 정상
- `0` - 비정상

**예시 쿼리:**
```promql
# 비정상 대상
kar98k_target_health == 0
```

## Grafana 대시보드

### 권장 패널

#### 트래픽 개요
```promql
# 현재 TPS
kar98k_current_tps

# 목표 vs 실제 TPS
kar98k_target_tps
kar98k_current_tps
```

#### 지연
```promql
# P50, P95, P99 지연
histogram_quantile(0.50, rate(kar98k_request_duration_seconds_bucket[5m]))
histogram_quantile(0.95, rate(kar98k_request_duration_seconds_bucket[5m]))
histogram_quantile(0.99, rate(kar98k_request_duration_seconds_bucket[5m]))
```

#### 에러 비율
```promql
# 에러 백분율
100 * sum(rate(kar98k_requests_total{status="error"}[5m])) /
sum(rate(kar98k_requests_total[5m]))
```

#### 시스템 헬스
```promql
# 활성 워커
kar98k_active_workers

# 큐 깊이
kar98k_queued_requests

# 진행 중인 요청
kar98k_requests_in_flight
```

## 종료 코드

| 코드 | 설명 |
|------|------|
| 0 | 성공 |
| 1 | 설정 에러 |
| 1 | 런타임 에러 |

## 시그널

| 시그널 | 동작 |
|--------|------|
| `SIGINT` | 우아한 종료 |
| `SIGTERM` | 우아한 종료 |
