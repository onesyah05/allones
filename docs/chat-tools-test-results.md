# Hasil pengujian AllOnes

Tanggal: 17 November 2026. Backend uji: http://127.0.0.1:8788. Port 8787 milik Antares tidak diubah. Data uji terisolasi di .test-e2e/home dan .test-e2e/workspace. Model chat: sense/sensenova-6.8-flash.

## Perbaikan terverifikasi

- Browser startup/tab mendapat timeout dan cancellation, gagal startup membersihkan pool.
- Profil Chromium mengikuti ALLONES_HOME agar instance terisolasi tidak memakai profil utama.
- Crash SSE saat banyak tool memancarkan event bersamaan direproduksi (panic bufio.Writer.Flush), lalu diperbaiki: hanya goroutine HTTP handler menulis ResponseWriter; event dari worker diteruskan lewat channel dan berhenti saat request dibatalkan.
- Test regresi stream: 20 emitter paralel x 100 event + done; test late emit setelah cancellation. Paket server diuji lima kali, lolos.

## Hasil aktual

- Chat browser example.com: navigate -> snapshot -> jawaban akhir -> done, lolos; uji akhir sekitar 19 detik.
- Stop API: stopped=true, tool menerima context canceled dan stream mencapai done.
- File: write_file, read_file, edit_file, glob, grep, list_files lolos.
- Shell: PowerShell Write-Output ALLONES_SHELL_OK, exit_code=0; process list lolos.
- Web: http_request dan web_fetch HTTP 200; web_search mengembalikan hasil IANA.
- Memory: save/search/delete lolos; todo set/update selesai.
- RAG: rag_index 1 file/1 chunk; rag_search menemukan ALLONES_SMOKE_456.
- Utilitas: codec SHA256, jwt_inspect, cvss, hexdump, strings_extract selesai dalam chat utility yang mencapai done.
- Agen: list_roles, session_search, skill list, board list lolos.
- delegate_task gagal HTTP 401: child memakai model default openai/gpt-5 tanpa API key, sementara parent diuji dengan sense. Belum diperbaiki/diuji ulang; perlu default/role model yang memiliki key valid.

## Batas cakupan

Belum menguji seluruh 45 tool. VPS/SSH, transfer, security scan aktif, REA, MCP, notifikasi, scheduling dan dialog interaktif tidak dijalankan end-to-end. Tidak ada klaim kelulusan untuk tool tersebut. Pengujian melalui API SSE, bukan klik dashboard browser.

Setelah crash SSE awal, Chromium uji tertinggal dan mengunci profil. Hanya pohon proses Chromium milik .test-e2e yang dihentikan; browser berhasil diuji ulang setelah itu.

## Pemeriksaan kode

- go test ./... -count=1 -timeout 120s: exit 0.
- go vet ./internal/server ./internal/agent ./internal/tools: exit 0.
- Binary allones.exe dibangun ulang dan backend perbaikan berjalan di 8788.
- Tambahan smoke_test.go tersedia; hasil total suite di atas berdasarkan snapshot file saat eksekusi, bukan jaminan tes yang ditulis sesudahnya sudah dijalankan.

Log event mentah lokal: .test-e2e/*-events.json. Folder .test-e2e masuk .gitignore karena konfigurasi uji berisi kredensial provider lokal; jangan dibagikan.
