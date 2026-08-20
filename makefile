generate-protocols: download-protocols
	go run ./cmd/waygenerator

download-protocols:
	curl "https://raw.githubusercontent.com/gitlab-freedesktop-mirrors/wayland/refs/heads/main/protocol/wayland.xml" -o protocols/wayland.xml
	curl "https://raw.githubusercontent.com/gitlab-freedesktop-mirrors/wayland-protocols/refs/heads/main/stable/xdg-shell/xdg-shell.xml" -o protocols/xdg-shell.xml
	curl "https://raw.githubusercontent.com/gitlab-freedesktop-mirrors/wayland-protocols/refs/heads/main/stable/viewporter/viewporter.xml" -o protocols/viewporter.xml
	curl "https://raw.githubusercontent.com/gitlab-freedesktop-mirrors/wayland-protocols/refs/heads/main/staging/fractional-scale/fractional-scale-v1.xml" -o protocols/fractional-scale-v1.xml
	curl "https://raw.githubusercontent.com/gitlab-freedesktop-mirrors/wayland-protocols/refs/heads/main/stable/tablet/tablet-v2.xml" -o protocols/tablet-v2.xml