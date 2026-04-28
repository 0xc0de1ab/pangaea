# 빌드 가이드

이 문서는 로컬 빌드, 산출물 경로, 버전 스탬프 규칙, 현재 CI 빌드 동작을 정리합니다.

## 빌드 매트릭스

저장소는 Makefile 기반 빌드 매트릭스를 사용합니다.

차원:

- OS: `linux`, `darwin`, `windows`
- 아키텍처: `amd64`, `arm64`
- variant: `debug`, `release`

지원 selector 형태:

- `make all`
- `make linux`
- `make arm64`
- `make release`
- `make linux-arm64`
- `make linux-release`
- `make arm64-release`
- `make linux-arm64-release`

간단한 요약은 다음으로 볼 수 있습니다.

```bash
make help
```

## 자주 쓰는 명령

Linux release 바이너리 하나 빌드:

```bash
make linux-amd64-release
```

모든 release 빌드:

```bash
make release
```

전체 빌드:

```bash
make all
```

생성 산출물 삭제:

```bash
make clean
```

## 산출물 경로

산출물은 다음 경로 아래에 생성됩니다.

```text
build/<os-label>-<arch>/<variant>/
```

예시:

- `build/linux-amd64/release/pangaeactl`
- `build/linux-arm64/debug/pangaeactl`
- `build/macos-arm64/release/pangaeactl`
- `build/windows-amd64/release/pangaeactl.exe`

참고:

- `darwin` 산출물은 `build/macos-<arch>/...` 아래에 생성됩니다
- Windows 산출물에는 `.exe`가 붙습니다

## Debug vs Release

`debug` 빌드:

- 심볼 유지
- `-gcflags=all=-N -l` 사용
- 디버깅과 로컬 분석에 적합

`release` 빌드:

- `CGO_ENABLED=0` 사용
- `-s -w`로 심볼 제거
- `-extldflags -static` 사용
- Linux에서는 정적 링크 바이너리를 목표로 함

## 버전 스탬프

현재 저장소의 base version:

```text
v0.9.0-202604.1
```

빌드 시 Makefile이 `scripts/version.sh`를 통해 short Git SHA를 뒤에 붙여 다음 형태를 만듭니다.

```text
vSEMVER-YYYYMM.seq.<commit-sha>
```

예시:

```text
v0.9.0-202604.1.f9e994e562eb
```

기본 short SHA 길이는 12자입니다.

관련 변수:

- `VERSION_BASE`
- `VERSION`
- `GIT_SHA`
- `SHA_LEN`

예시:

```bash
VERSION_BASE=v0.9.1-202604.2 make linux-amd64-release
GIT_SHA=abcdef123456 make linux-amd64-release
SHA_LEN=8 make linux-amd64-release
```

## 테스트 및 정리 타깃

사용 가능한 비빌드 타깃:

- `make test`
- `make race`
- `make integration`
- `make lint`
- `make fmt`
- `make vet`
- `make tidy`

예시:

```bash
make test
make race
make vet
```

## CI 빌드 동작

현재 GitHub Actions는:

- E2E package를 `go test ./e2e`로 먼저 실행
- `cmd/pangaeactl`과 `e2e`를 제외한 package 대상으로 `go test -covermode=atomic` coverage 측정
- 여러 타깃에 대한 release 빌드 수행
- `vSEMVER-YYYYMM.seq` 형태의 tag push 시 GitHub Release 생성
- `main`에서 coverage badge 갱신

## 태그 기반 릴리스

저장소에는 tag push 기반 release workflow도 있습니다.

기대 tag 형식:

```text
vSEMVER-YYYYMM.seq
```

예시:

```text
v0.9.0-202604.1
```

이 형식의 tag가 push되면 GitHub Actions가 다음을 수행합니다.

- 해당 tag의 소스를 checkout
- `VERSION_BASE=<tag> make release`로 전체 release 매트릭스 빌드
- 각 OS-ARCH release 바이너리를 개별 zip 파일로 패키징
- 해당 tag에 대한 GitHub Release 생성 또는 갱신
- zip 파일들을 release asset으로 업로드

asset 이름 패턴:

```text
pangaeactl_<tag>_<os>-<arch>.zip
```

예시:

- `pangaeactl_v0.9.0-202604.1_linux-amd64.zip`
- `pangaeactl_v0.9.0-202604.1_macos-arm64.zip`
- `pangaeactl_v0.9.0-202604.1_windows-amd64.zip`

## 운영 메모

- 배포 바이너리 비교 시 버전 문자열보다 파일 해시가 더 확실할 수 있습니다
- 이 저장소에서는 `linux-arm64-release`, `linux-amd64-release`가 가장 자주 쓰이는 타깃입니다
- 빌드와 배포 문서는 분리되어 있으니, 런타임 셋업은 [deploy.md](./deploy.md)를 보세요
