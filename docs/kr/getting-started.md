# 시작하기

## 설치

### 소스에서 빌드

```bash
# 저장소 클론
git clone https://github.com/kar98k/kar98k.git
cd kar98k

# 빌드
make build

# 설치 확인
./bin/kar98k -version
```

### Docker 사용

```bash
# Docker 이미지 빌드
docker build -t kar98k:latest .

# 또는 docker-compose 사용
docker-compose up -d
```

### 사전 빌드 바이너리

[Releases](https://github.com/kar98k/kar98k/releases) 페이지에서 사전 빌드된 바이너리를 다운로드하세요.

## 빠른 시작

### 1. 설정 파일 생성

`kar98k.yaml` 파일을 생성합니다:

```yaml
targets:
  - name: my-api
    url: http://localhost:8080/api/health
    protocol: http
    method: GET
    weight: 100
    timeout: 10s

controller:
  base_tps: 100
  max_tps: 500

pattern:
  poisson:
    enabled: true
    lambda: 0.1
    spike_factor: 2.0
  noise:
    enabled: true
    amplitude: 0.1

metrics:
  enabled: true
  address: ":9090"
```

### 2. kar98k 실행

```bash
./bin/kar98k -config kar98k.yaml
```

### 3. 메트릭 모니터링

브라우저에서 `http://localhost:9090/metrics`를 열어 Prometheus 메트릭을 확인합니다.

## 기본 설정

### 타겟

트래픽을 보낼 엔드포인트를 정의합니다:

```yaml
targets:
  - name: api-endpoint      # 고유 식별자
    url: http://host:port/path
    protocol: http          # http, http2, 또는 grpc
    method: GET             # HTTP 메서드
    headers:                # 선택적 헤더
      Authorization: "Bearer token"
    body: '{"key": "value"}'  # 선택적 요청 본문
    weight: 100             # 부하 분배를 위한 상대적 가중치
    timeout: 10s            # 요청 타임아웃
```

### 트래픽 패턴

트래픽 생성 방식을 제어합니다:

```yaml
controller:
  base_tps: 100           # 기본 TPS
  max_tps: 1000           # 최대 TPS 상한
  ramp_up_duration: 30s   # 시작 시 기본 TPS에 도달하는 시간

pattern:
  poisson:
    enabled: true
    lambda: 0.1           # 스파이크 빈도 (초당 스파이크 수)
    spike_factor: 3.0     # 스파이크 시 TPS 배율
  noise:
    enabled: true
    amplitude: 0.15       # 무작위 변동 범위 (±15%)
```

## 확인

### 헬스 엔드포인트 확인

```bash
curl http://localhost:9090/healthz
# 출력: ok
```

### 메트릭 확인

```bash
curl http://localhost:9090/metrics | grep kar98k
```

주요 모니터링 메트릭:
- `kar98k_requests_total` - 총 전송 요청 수
- `kar98k_current_tps` - 현재 실제 TPS
- `kar98k_target_tps` - 목표 TPS 설정
- `kar98k_spike_active` - 스파이크 활성 여부

## 다음 단계

- [설정 레퍼런스](configuration.md) - 전체 설정 옵션
- [아키텍처](architecture.md) - kar98k 작동 방식 심층 분석
- [API 레퍼런스](api-reference.md) - 메트릭 및 엔드포인트
