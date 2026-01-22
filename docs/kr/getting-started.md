# 시작하기

## 설치

### 바이너리 다운로드 (권장)

운영체제에 맞는 최신 릴리즈를 다운로드하세요:

```bash
# Linux (amd64)
curl -LO https://github.com/rlaope/kar98k/releases/latest/download/kar98k-linux-amd64
chmod +x kar98k-linux-amd64
sudo mv kar98k-linux-amd64 /usr/local/bin/kar

# Linux (arm64)
curl -LO https://github.com/rlaope/kar98k/releases/latest/download/kar98k-linux-arm64
chmod +x kar98k-linux-arm64
sudo mv kar98k-linux-arm64 /usr/local/bin/kar

# macOS (Apple Silicon / M1, M2, M3)
curl -LO https://github.com/rlaope/kar98k/releases/latest/download/kar98k-darwin-arm64
chmod +x kar98k-darwin-arm64
sudo mv kar98k-darwin-arm64 /usr/local/bin/kar

# macOS (Intel)
curl -LO https://github.com/rlaope/kar98k/releases/latest/download/kar98k-darwin-amd64
chmod +x kar98k-darwin-amd64
sudo mv kar98k-darwin-amd64 /usr/local/bin/kar

# Windows (PowerShell)
Invoke-WebRequest -Uri https://github.com/rlaope/kar98k/releases/latest/download/kar98k-windows-amd64.exe -OutFile kar.exe
```

### Docker 사용

```bash
# GitHub Container Registry에서 이미지 받기
docker pull ghcr.io/rlaope/kar98k:latest

# 실행
docker run --rm -it ghcr.io/rlaope/kar98k:latest version
```

### 소스에서 빌드

```bash
# 저장소 클론
git clone https://github.com/rlaope/kar98k.git
cd kar98k

# 빌드
make build

# 바이너리 위치: ./bin/kar
./bin/kar version
```

### 설치 확인

```bash
kar version
```

## 빠른 시작

### 인터랙티브 모드 (권장)

```bash
kar start
```

인터랙티브 TUI가 실행되며 4단계로 설정을 진행합니다:

1. **타겟 설정** - URL, HTTP 메서드, 프로토콜 선택
2. **트래픽 설정** - Base TPS, Max TPS 설정
3. **패턴 설정** - Poisson Lambda, Spike Factor, Noise Amplitude, 스케줄
4. **검토 & 실행** - 설정 확인 후 트리거 당기기!

### Headless 모드

자동화를 위해 설정 파일로 실행:

```bash
kar run --config kar98k.yaml
```

### 데몬 모드

백그라운드 서비스로 실행:

```bash
# 데몬 시작
kar start --daemon

# 상태 확인
kar status

# 로그 보기
kar logs -f

# 트래픽 시작
kar trigger

# 트래픽 일시정지
kar pause

# 데몬 중지
kar stop
```

## 명령어

| 명령어 | 설명 |
|--------|------|
| `kar start` | 인터랙티브 TUI 실행 |
| `kar start --daemon` | 백그라운드 데몬으로 시작 |
| `kar run --config <file>` | 설정 파일로 headless 실행 |
| `kar status` | 데몬 상태 확인 |
| `kar logs [-f]` | 로그 보기 (-f: 실시간) |
| `kar trigger` | 트래픽 생성 시작 |
| `kar pause` | 트래픽 생성 일시정지 |
| `kar stop` | 데몬 중지 |
| `kar version` | 버전 정보 |

## 설정 파일 예시

headless/데몬 모드용 YAML 설정 파일:

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
