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

#### TUI 키보드 단축키

| 키 | 동작 |
|----|------|
| `Tab` / `↓` | 다음 필드 |
| `Shift+Tab` / `↑` | 이전 필드 |
| `Enter` | 다음 화면 / 선택 |
| `Esc` | 이전 화면 |
| `Q` 또는 `Ctrl+C` | 종료 후 리포트 표시 (Running 화면에서) |

#### 테스트 리포트

테스트 종료 시 (`Q` 또는 `Ctrl+C`) 상세 리포트가 표시됩니다:

- **Overview**: 테스트 시간, 총 요청 수, 성공률, 평균/최대 TPS
- **Latency Distribution**: Min, Avg, Max, P50, P95, P99
- **Latency Histogram**: 응답 시간 분포 시각화
- **Status Codes**: HTTP 상태 코드별 카운트
- **Timeline Summary**: 5초 간격 상세 내역 (spike 감지 포함)

### 실시간 로그

테스트 실행 중 다른 터미널에서 이벤트 모니터링:

```bash
# 다른 터미널에서
kar logs -f

# 또는 직접
tail -f /tmp/kar98k/kar98k.log
```

로그 이벤트 종류:
- `EVENT: SPIKE START/END` - 스파이크 감지
- `EVENT: New peak TPS` - 새로운 최고 TPS
- `STATUS:` - 주기적 상태 (10초마다)
- `WARNING:` - 에러 급증
- `SUMMARY:` - 종료 시 최종 요약

### 실행 중인 테스트 중지

```bash
# 다른 터미널에서
kar stop
```

실행 결과:
1. 실행 중인 kar 인스턴스에 중지 신호 전송
2. 실행 중이던 터미널에 테스트 리포트 표시
3. stop 명령을 실행한 터미널에 요약 표시

### Headless 모드

자동화를 위해 설정 파일로 실행:

```bash
kar run --config kar.yaml
```

### 데모 서버

테스트용 데모 HTTP 서버가 포함되어 있습니다:

```bash
# 데모 서버 빌드 및 실행
make run-server

# 서버 주소: http://localhost:8080
# 엔드포인트: /health, /api/users, /api/stats, /api/echo
```

## 명령어

| 명령어 | 설명 |
|--------|------|
| `kar start` | 인터랙티브 TUI 실행 |
| `kar run --config <file>` | 설정 파일로 headless 실행 |
| `kar stop` | 실행 중인 kar 중지 |
| `kar logs` | 최근 로그 보기 |
| `kar logs -f` | 실시간 로그 보기 |
| `kar logs -n 50` | 마지막 50줄 보기 |
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
