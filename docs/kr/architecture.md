# 아키텍처

이 문서에서는 kar98k의 내부 아키텍처를 설명합니다.

## 시스템 아키텍처

```
┌─────────────────────────────────────────────────────────────────────┐
│                              main.go                                 │
│                         (애플리케이션 진입점)                         │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                    ┌───────────────┼───────────────┐
                    ▼               ▼               ▼
        ┌───────────────┐  ┌───────────────┐  ┌───────────────┐
        │   Controller  │  │  Worker Pool  │  │Health Checker │
        │               │  │               │  │               │
        │ ┌───────────┐ │  │ ┌───────────┐ │  │ ┌───────────┐ │
        │ │ Scheduler │ │  │ │  Limiter  │ │  │ │  Metrics  │ │
        │ └───────────┘ │  │ └───────────┘ │  │ └───────────┘ │
        └───────┬───────┘  └───────┬───────┘  └───────────────┘
                │                  │
                ▼                  ▼
        ┌───────────────┐  ┌───────────────┐
        │Pattern Engine │  │   Protocols   │
        │               │  │               │
        │ ┌───────────┐ │  │ ┌───────────┐ │
        │ │  Poisson  │ │  │ │   HTTP    │ │
        │ ├───────────┤ │  │ ├───────────┤ │
        │ │   Noise   │ │  │ │   gRPC    │ │
        │ └───────────┘ │  │ └───────────┘ │
        └───────────────┘  └───────────────┘
```

## 핵심 컴포넌트

### 1. Controller (`internal/controller/`)

Controller는 kar98k의 두뇌로, 모든 트래픽 생성을 오케스트레이션합니다.

**책임:**
- Pattern Engine과 Worker Pool 간 조정
- 시간대별 스케줄링 적용
- 시작 시 램프업 관리
- 우아한 종료 처리

**주요 메서드:**
```go
func (c *Controller) Start(ctx context.Context)  // 트래픽 생성 시작
func (c *Controller) Stop()                       // 우아한 종료
func (c *Controller) GetStatus() Status           // 현재 상태
```

**제어 루프:**
1. **램프업 루프**: TPS를 0에서 base_tps까지 점진적으로 증가
2. **제어 루프**: 패턴을 기반으로 100ms마다 목표 TPS 업데이트
3. **생성 루프**: 워커 풀에 지속적으로 작업 제출

### 2. Scheduler (`internal/controller/scheduler.go`)

시간대별 TPS 배율을 관리합니다.

**작동 방식:**
```go
// 현재 시간의 배율 가져오기
multiplier := scheduler.GetMultiplier()

// 기본 TPS에 적용
effectiveTPS := baseTPS * multiplier
```

**스케줄 해석:**
- 스케줄의 나중 항목이 우선순위를 가짐
- 어떤 스케줄 항목에도 없는 시간은 배율 1.0 사용

### 3. Pattern Engine (`internal/pattern/`)

수학적 모델을 사용하여 현실적인 트래픽 패턴을 생성합니다.

#### 포아송 스파이크 생성기

무작위 스파이크 타이밍을 위한 포아송 분포 사용:

```go
// 역변환 샘플링
// t = -ln(U) / λ 여기서 U ~ Uniform(0,1)
interval := -math.Log(rand.Float64()) / lambda
```

**스파이크 생명주기:**
```
      TPS
       ▲
       │    ╭──╮
스파이크│   ╱    ╲
 배율  │  ╱      ╲
       │ ╱        ╲
기본 ──┼╱──────────╲────────
       │           │
       └─────┴─────┴─────▶ 시간
        램프  피크  램프
         업         다운
```

#### 노이즈 생성기

스프링-댐퍼 시스템을 사용하여 지속적인 마이크로 변동을 추가합니다:

```go
force := springConstant * (target - current)
velocity = velocity * damping + force
current += velocity
```

### 4. Worker Pool (`internal/worker/`)

요청 실행을 위한 고루틴을 관리합니다.

**구성 요소:**
- **작업 큐**: 대기 중인 요청을 위한 버퍼드 채널
- **레이트 리미터**: TPS 제어를 위한 토큰 버킷
- **고루틴 풀**: 고정된 수의 워커 고루틴

**흐름:**
```
Submit(job) → 큐 → 워커 → RateLimiter.Wait() → Client.Do() → 메트릭
```

**레이트 리미팅:**
`golang.org/x/time/rate` 토큰 버킷 알고리즘 사용:
- 목표 TPS 속도로 토큰 추가
- 각 요청은 하나의 토큰을 소비
- 토큰이 없으면 요청 대기

### 5. Protocol Clients (`pkg/protocol/`)

프로토콜별 클라이언트 구현입니다.

**인터페이스:**
```go
type Client interface {
    Do(ctx context.Context, req *Request) *Response
    Close() error
}
```

**HTTP 클라이언트 특징:**
- 연결 풀링
- Keep-alive 지원
- 버퍼 재사용을 위한 `sync.Pool`
- HTTP/2 스트림 멀티플렉싱

**gRPC 클라이언트 특징:**
- 대상별 연결 캐싱
- Keepalive 설정
- 표준 헬스 체크 프로토콜

### 6. Health Checker (`internal/health/`)

대상 헬스를 모니터링하고 메트릭을 수집합니다.

**헬스 체크 흐름:**
1. 주기적 헬스 체크 (설정 가능한 간격)
2. 각 대상에 GET 요청 전송
3. 에러 또는 상태 코드 >= 400이면 비정상으로 표시
4. 비정상 대상은 트래픽에서 제외

**Prometheus 메트릭:**
| 메트릭 | 타입 | 설명 |
|--------|------|------|
| `kar98k_requests_total` | Counter | 대상/상태별 총 요청 수 |
| `kar98k_request_duration_seconds` | Histogram | 요청 지연 |
| `kar98k_current_tps` | Gauge | 실제 TPS |
| `kar98k_target_tps` | Gauge | 목표 TPS |
| `kar98k_active_workers` | Gauge | 활성 워커 수 |
| `kar98k_spike_active` | Gauge | 스파이크 활성 (1/0) |
| `kar98k_target_health` | Gauge | 대상 헬스 (1/0) |

## 데이터 흐름

### 요청 생성 흐름

```
1. Controller.generateLoop()
   │
   ├─▶ 대상 선택 (가중치 기반 랜덤)
   │
   ├─▶ 대상 헬스 확인
   │
   ├─▶ Job{Target, Client} 생성
   │
   └─▶ Pool.Submit(job)
        │
        ├─▶ 큐 (버퍼드 채널)
        │
        └─▶ 워커 고루틴
             │
             ├─▶ RateLimiter.Wait()
             │
             ├─▶ Client.Do(request)
             │
             └─▶ Metrics.RecordRequest()
```

### TPS 계산 흐름

```
1. Controller.updateTPS() [100ms마다]
   │
   ├─▶ Scheduler.GetMultiplier()
   │    └─▶ 현재 시간을 스케줄과 비교
   │
   ├─▶ Engine.CalculateTPS(scheduleMultiplier)
   │    │
   │    ├─▶ baseTPS * scheduleMultiplier
   │    │
   │    ├─▶ * Poisson.Multiplier()
   │    │    └─▶ 스파이크 상태 확인, 램프 계산
   │    │
   │    └─▶ * Noise.Multiplier()
   │         └─▶ 스프링-댐퍼 계산
   │
   └─▶ Pool.SetRate(tps)
        └─▶ 레이트 리미터 업데이트
```

## 동시성 모델

### 고루틴

1. **메인 고루틴**: 시그널 처리, 생명주기 관리
2. **제어 루프**: TPS 업데이트 (1개 고루틴)
3. **생성 루프**: 작업 제출 (1개 고루틴)
4. **워커 풀**: 요청 실행 (N개 고루틴, 설정 가능)
5. **헬스 체커**: 주기적 체크 (1개 고루틴)
6. **메트릭 서버**: HTTP 서버 (net/http에서 관리)

### 동기화

- **레이트 리미터**: 스레드 안전 토큰 버킷
- **메트릭**: 스레드 안전 Prometheus 수집기
- **Pattern Engine**: 상태 접근을 위한 `sync.RWMutex`
- **작업 큐**: Go 채널 (본질적으로 스레드 안전)

## 우아한 종료

```
1. SIGINT/SIGTERM 수신
   │
   ├─▶ 컨텍스트 취소
   │
   ├─▶ Controller.Stop()
   │    └─▶ 제어/생성 루프 대기
   │
   ├─▶ HealthChecker.Stop()
   │
   ├─▶ Pool.Drain(timeout)
   │    └─▶ 진행 중인 요청 대기
   │
   ├─▶ Pool.Stop()
   │    └─▶ 작업 채널 닫기, 워커 대기
   │
   └─▶ MetricsServer.Shutdown()
```
