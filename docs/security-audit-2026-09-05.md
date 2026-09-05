# Backend security review — 05/09/2026

> Update: the findings below describe the original audit state. Fixes, completed production rollout and the remaining trusted-proxy configuration limitation are recorded in [security-fixes-2026-09-05.md](security-fixes-2026-09-05.md).

Phạm vi: Go backend tại `D:\Liquor-store\apps\api-go`, HEAD `0b0468f`, cùng contract và phần proxy frontend liên quan. Đây là audit để chuẩn bị vá, không phải chứng nhận an toàn hoặc kiểm thử xâm nhập production.

Đã đọc source theo ba nhóm độc lập: auth/session; API/store isolation; notification/webhook/video. Đã chạy unit tests, vet, hai harness local với dữ liệu giả và govulncheck. Không đọc secret trong `.env`, không chạy database integration, không gửi Telegram/WhatsApp thật, không thay đổi database/deployment. Source ứng dụng và dependencies chưa được sửa.

## Kết luận và thứ tự vá

Có bốn vấn đề ứng dụng mức P2 cần xử lý, một điểm rate-limit P2 phụ thuộc ingress, và cảnh báo toolchain cần cập nhật. P2 ở đây là ưu tiên vá trung bình, không phải điểm CVSS. Chưa có bằng chứng người ngoài không đăng nhập đang chiếm được tài khoản hoặc dữ liệu production.

| ID | Vấn đề | Bằng chứng | Hành động |
|---|---|---|---|
| SEC-01 | API tạo store tự cấp OWNER cho mọi người dùng có JWT | Source và contract | Khóa quyền tạo store, kiểm tra ACTIVE |
| SEC-02 | Notification credentialRef chọn được biến môi trường ngoài phạm vi | Source + localhost PoC | Mapping credential do server quản lý, scope theo store/provider |
| SEC-03 | OPERATOR sửa được kết quả alert đã RESOLVED | Source/SQL | Enforce state transitions và giữ audit history |
| SEC-04 | Không giới hạn TEST notification, dùng chung queue với ALERT | Source/SQL | Quota, giới hạn pending TEST và ưu tiên ALERT |
| SEC-05 | Tin IP đầu tiên trong X-Forwarded-For | Local harness; production có điều kiện | Xác định trusted proxies và chuẩn hóa client IP |
| DEP-01 | Go local 1.26.5 còn advisory ở standard library | govulncheck Windows + Linux target | Nâng toolchain đã vá, rebuild và quét lại |

Ưu tiên xử lý SEC-01 + SEC-02 cùng đợt vì chúng kết hợp được: một OPERATOR tạo store mới, nhận OWNER của store đó, rồi truy cập API cấu hình notification vốn chỉ giới hạn OWNER. Đây không phải cách chiếm OWNER của store khác.

## SEC-01 — P2: Tạo store không kiểm tra quyền hoặc trạng thái user

Vị trí:

- [server.go:75](../apps/api-go/internal/server/server.go#L75): `POST /stores` chỉ đi qua JWT authentication.
- [stores.go:57](../apps/api-go/internal/server/stores.go#L57): `createStore` không kiểm tra quyền tạo store/ACTIVE.
- [stores.go:90](../apps/api-go/internal/server/stores.go#L90): INSERT membership luôn cấp OWNER.
- [api-contract.md:20](api-contract.md#L20): contract quy định platform bootstrap / owner.

Điều kiện và tác động: một tài khoản OPERATOR đang có access JWT hợp lệ gửi dữ liệu store hợp lệ sẽ đi tới transaction tạo store và membership OWNER. `REGISTER_ENABLED=false` và `MEMBER_MANAGEMENT_ENABLED=false` không khóa route này. User bị SUSPENDED nhưng còn JWT cũng vào được handler; quyền store-scoped khác vẫn bị `requireRole` chặn vì hàm đó kiểm tra ACTIVE.

Khả năng chiếm store có sẵn không được chứng minh và không được suy ra từ lỗi này. Tác động chắc chắn ở code là tạo tài nguyên trái policy, tự có store OWNER, và mở đường tới các chức năng dành cho owner của store mới.

Hướng vá:

- Tách bootstrap thành lệnh quản trị/route mặc định tắt, hoặc yêu cầu quyền tạo store riêng do server cấp.
- Kiểm tra user ACTIVE trước protected handlers, không chỉ trong `requireRole`.
- Không dùng “đã đăng nhập” làm quyền tạo tenant/store.

Test cần có: OPERATOR, tài khoản không có quyền tạo store và SUSPENDED nhận 403/401 phù hợp, không có INSERT; đường bootstrap được phép vẫn hoạt động. Chưa chạy kịch bản này trên PostgreSQL trong audit.

Ghi nhận liên quan: `listStores`, `userProfile`, `updateUserProfile` cũng chưa kiểm tra ACTIVE tập trung. Phạm vi còn dùng được với JWT cũ giới hạn ở các route này, không phải toàn bộ dữ liệu camera/alert.

## SEC-02 — P2: Credential reference không có allowlist hoặc scope

Vị trí:

- [notifications.go:168](../apps/api-go/internal/server/notifications.go#L168): validation chỉ kiểm tra định dạng reference.
- [notifications.go:259](../apps/api-go/internal/server/notifications.go#L259): lưu reference do client cung cấp.
- [credential_resolver.go:34](../apps/api-go/internal/notifications/credential_resolver.go#L34): mọi tên env hợp lệ được đọc bằng `os.Getenv`.
- [telegram_sender.go:148](../apps/api-go/internal/notifications/telegram_sender.go#L148): giá trị được đưa vào URL provider.
- [whatsapp_sender.go:220](../apps/api-go/internal/notifications/whatsapp_sender.go#L220): giá trị được đưa vào Authorization.

Một OWNER của store có thể chọn `env://JWT_ACCESS_SECRET` thay vì credential provider. Khi worker xử lý, resolver đọc biến đó mà không biết store/provider đang được phép dùng secret nào. Tương tự, nếu nhiều credential cho các cửa hàng cùng tồn tại trong environment, người biết tên biến có thể chọn credential không thuộc store của mình.

PoC local đã pass: dùng `t.Setenv` với secret giả, `ValidCredentialRef("env://JWT_ACCESS_SECRET")` chấp nhận, sender gửi giá trị giả vào đường dẫn request tới localhost `httptest`. Không dùng secret thật, không gọi provider thật.

Giới hạn kết luận: host provider đang cố định và redirect đã bị chặn. Bằng chứng là secret có thể bị gửi nhầm tới provider, không phải attacker đọc thẳng secret từ API response hay tự chọn một host để nhận secret. Việc dùng credential của store khác cần credential đó tồn tại và tên biến được biết/đoán đúng.

Hướng vá:

- API chỉ nhận opaque credential ID; mapping ID → secret reference do server quản lý.
- Bind credential với store, provider và provider account; kiểm tra lúc lưu/enqueue và lúc worker resolve.
- Bản vá tối thiểu phải có allowlist cụ thể, không chỉ kiểm tra tiền tố tên biến. Từ chối JWT, DB, ingest, app-secret và biến ngoài phạm vi.

Test cần có: reference ngoài allowlist bị từ chối trước đọc env/gọi HTTP; credential hợp lệ nhưng sai store/provider bị từ chối; credential được cấp đúng vẫn gửi qua mock provider.

## SEC-03 — P2: OPERATOR có thể ghi đè kết quả xử lý của quản lý

Vị trí: [alerts.go:180](../apps/api-go/internal/server/alerts.go#L180) và [alerts.go:218](../apps/api-go/internal/server/alerts.go#L218).

`resolve` yêu cầu MANAGER, nhưng `dismiss`/`acknowledge` chỉ yêu cầu OPERATOR. Câu UPDATE không ràng buộc trạng thái hiện tại. Vì vậy OPERATOR của cùng store có thể dismiss alert đã RESOLVED: status chuyển DISMISSED, người/thời điểm xử lý bị ghi đè và request `{}` làm note cũ thành NULL. Incident không còn thuộc tập RESOLVED mà dashboard tải cho Confirmed. Đây là thay đổi trạng thái, không xóa hàng database.

Không tìm thấy lịch sử thao tác alert append-only trong schema để giữ lại quyết định bị ghi đè. Bằng chứng là luồng quyền và câu SQL; không gọi thao tác này trên dữ liệu thật.

Hướng vá:

- Enforce state machine ở backend và điều kiện UPDATE/transaction để tránh cả thao tác đồng thời.
- OPERATOR chỉ xử lý trạng thái được phép; thay đổi quyết định cuối phải cần MANAGER/OWNER theo policy.
- Giữ tính năng `Confirmed by mistake?` cho người được phép, kèm audit actor, trạng thái cũ/mới, note, thời điểm. Không dựa vào việc UI ẩn nút.

Test cần có: OPERATOR không đổi RESOLVED; người quản lý thực hiện correction đúng policy và có lịch sử; hai request đồng thời không làm mất audit hoặc ghi đè một transition không còn hợp lệ.

## SEC-04 — P2: TEST notification không có giới hạn volume

Vị trí:

- [notifications.go:424](../apps/api-go/internal/server/notifications.go#L424): UUID mới được đưa thẳng tới enqueue.
- [outbox.go:188](../apps/api-go/internal/notifications/outbox.go#L188): dedupe theo endpoint/requestId, UUID khác tạo delivery khác.
- [worker.go:162](../apps/api-go/internal/notifications/worker.go#L162): TEST và ALERT của mọi store dùng chung queue theo thời gian, không ưu tiên ALERT.

OWNER có endpoint enabled có thể liên tục đổi requestId để tạo nhiều TEST jobs. Dedupe hiện chống lặp cùng request, không hạn chế số lần thử. Queue có thể tăng ngay cả khi worker tắt. Khi worker bật và credential hợp lệ, đây còn là lạm dụng quota/provider và có thể phát sinh chi phí. Backlog TEST tạo trước ALERT sẽ tiêu tốn batch/công suất chung, làm chậm cảnh báo an ninh ở store khác. Chưa thực hiện flood và chưa đo độ trễ hay chi phí production.

Hướng vá: giới hạn nguyên tử theo user/store/endpoint, quota theo cửa sổ thời gian và maximum pending TEST; trả 429 khi vượt. Worker cần ưu tiên/dành công suất ALERT và fairness giữa stores. Không dùng IP-only limit thay quota vì owner có thể gọi từ nhiều IP.

Test cần có: đổi UUID không vượt quota; nhiều request đồng thời không tạo vượt giới hạn; backlog TEST không lấy hết công suất dành cho emergency ALERT.

## SEC-05 — P2, có điều kiện: Client IP từ X-Forwarded-For không đáng tin

Vị trí: [rate_limit.go:84](../apps/api-go/internal/server/rate_limit.go#L84).

Khi `TRUST_PROXY=true`, code lấy phần tử đầu tiên của X-Forwarded-For, không kiểm tra TCP peer có phải proxy tin cậy và không xử lý chuỗi proxy từ phía server. Nếu ingress giữ hoặc nối thêm vào header do client gửi, client đổi giá trị đầu để tạo bucket rate limit mới. Nếu API có đường truy cập trực tiếp, header cũng không được bỏ qua.

Harness local dùng nguyên source limiter, cùng TCP peer và cùng IP thật được nối cuối:

- `TrustProxy=false`: chỉ 10/100 request đi qua giới hạn login.
- `TrustProxy=true`: 100/100 request đi qua khi thay IP đầu.
- Tạo 10.000 bucket làm IP mới không liên quan nhận 429 vì map đã đầy.

Đây là mô phỏng handler local, không phát traffic flood ra mạng. Chưa xác minh ingress Render hiện có ghi đè XFF hay không; không kết luận production đã bị bypass. Proxy Ketch copy incoming headers, nhưng điều đó chưa chứng minh toàn bộ chuỗi Cloudflare→Render giữ nguyên header tới Go.

Hướng vá: cấu hình trusted proxies/CIDRs hoặc hop policy chính xác, bỏ qua forwarded header từ peer không tin cậy, duyệt từ phải sang trái tới IP đầu tiên không phải proxy và chuẩn hóa IP. Một lựa chọn khác là ingress ghi đè header đã được xác minh bằng test hạ tầng. Không đơn giản chuyển sang một header client khác.

## DEP-01 — Toolchain: advisory đã có bản vá

Máy đang dùng `go1.26.5 windows/amd64`; [go.mod:5](../apps/api-go/go.mod#L5) và script portable cũng gợi ý 1.26.5. `govulncheck ./...` trả kết quả có 6 advisory ở symbol-level; quét lại target Linux/amd64 cùng toolchain cho cùng kết quả:

| Advisory | Package | Bản vá trong nhánh 1.26 |
|---|---|---|
| [GO-2026-6218](https://pkg.go.dev/vuln/GO-2026-6218) | net/url | 1.26.6 |
| [GO-2026-6090](https://pkg.go.dev/vuln/GO-2026-6090) | crypto/tls | 1.26.6 |
| [GO-2026-6089](https://pkg.go.dev/vuln/GO-2026-6089) | net/http | 1.26.6 |
| [GO-2026-6088](https://pkg.go.dev/vuln/GO-2026-6088) | encoding/xml | 1.26.6 |
| [GO-2026-5972](https://pkg.go.dev/vuln/GO-2026-5972) | encoding/asn1 | 1.26.6 |
| [GO-2026-5026](https://pkg.go.dev/vuln/GO-2026-5026) | net/http / bundled idna | 1.26.6 |

Không diễn giải sáu kết quả này thành sáu exploit production đã xác nhận. Phân tích call graph có thể đi qua branch/interface không dùng trong deployment:

- GO-2026-6089 cần bật unencrypted HTTP/2; cấu hình server đã đọc không bật tính năng này.
- TLS inbound trong deployment lịch sử terminate tại ingress; Go dùng `ListenAndServe`. Outbound TLS vẫn có, nhưng cần đánh giá trust boundary của peer.
- Trace XML đi qua pgx codec không chứng minh HTTP input hiện tại được decode như XML.
- URL resolution có trace qua HTTP client nhưng provider/origin cố định và redirect bị chặn.

Tool còn báo 1 advisory ở package-level và 22 ở module-level không thấy code gọi tới symbol bị ảnh hưởng. Không xếp các lỗi SSH/OpenPGP không dùng thành lỗ hổng ứng dụng hiện tại.

Hướng xử lý: nâng bản Go local/CI đã vá còn được hỗ trợ (ít nhất 1.26.6 nếu giữ nhánh 1.26), cập nhật script/toolchain nhất quán, rebuild và quét lại. `toolchain` không chứng minh phiên bản binary đang chạy trên Render; lịch sử từng ghi Render build bằng Go 1.27.0. Cần kiểm tra build/runtime thực tế trước khi kết luận production còn ảnh hưởng.

## Kiểm tra đã chạy và giới hạn

- `go test -count=1 ./...`: pass. Tắt rõ các cờ live/provider/production/database integration trước khi chạy.
- `go vet ./...`: pass.
- Hai harness local: credential boundary với secret giả và forwarded-IP limiter: pass, tái hiện các hành vi nêu trên.
- `govulncheck` Windows và Linux target: có findings như DEP-01; không phải scan sạch.
- Source tracing: JWT giới hạn HS256/issuer/audience/expiry, Origin checks, refresh rotation, parameterized SQL/store filters, AI store-camera-zone validation, webhook HMAC và fixed evidence origin đã có kiểm soát. Chưa thấy đường SQL injection, cross-store IDOR hoặc secure-link SSRF trực tiếp trong phần đã rà.
- Không quét tải hoặc khai thác production, không kiểm tra secret rotation/billing/Neon privileges hiện thời, không xác nhận tất cả đường code bằng database integration. Test xanh không có nghĩa không còn lỗ hổng.

Các harness nằm ngoài repo trong thư mục Temp, dùng source copy và dữ liệu giả. File audit này là thay đổi tài liệu duy nhất của đợt review; chưa vá source, commit, push hay deploy.
