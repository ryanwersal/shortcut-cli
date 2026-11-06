#!/usr/bin/env python3
"""Build deterministic release archives and the matching Homebrew cask."""
import argparse
import gzip
import hashlib
import io
from pathlib import Path
import re
import tarfile

ROOT = Path(__file__).resolve().parent.parent
FILES = ("sc", "LICENSE", "README.md", "sc.bash", "_sc", "sc.fish")


def validate_version(version):
    if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version):
        raise ValueError("VERSION must be a stable major.minor.patch version")
    return version


def archive(directory, destination):
    """Fix ownership, permissions, timestamps, and gzip headers for reproducibility."""
    with destination.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as output:
                for name in FILES:
                    content = (directory / name).read_bytes()
                    entry = tarfile.TarInfo(name)
                    entry.size = len(content)
                    entry.mode = 0o755 if name == "sc" else 0o644
                    output.addfile(entry, io.BytesIO(content))


def render_cask(version, checksums):
    validate_version(version)
    for checksum in checksums.values():
        if not re.fullmatch(r"[0-9a-f]{64}", checksum):
            raise ValueError("invalid SHA-256 checksum")
    return f'''# Updated by shortcut-cli's release workflow.
cask "shortcut-cli" do
  arch arm: "arm64", intel: "amd64"

  version "{version}"
  sha256 arm:   "{checksums['arm64']}",
         intel: "{checksums['amd64']}"

  url "https://github.com/ryanwersal/shortcut-cli/releases/download/v#{{version}}/sc_#{{version}}_darwin_#{{arch}}.tar.gz"
  name "Shortcut CLI"
  desc "CLI for Shortcut with explicit read and write operations"
  homepage "https://github.com/ryanwersal/shortcut-cli"

  livecheck do
    url :url
    strategy :github_latest
  end

  depends_on macos: :ventura

  binary "sc"
  bash_completion "sc.bash"
  zsh_completion "_sc"
  fish_completion "sc.fish"
end
'''


def package(version, dist):
    checksums = {}
    manifest = []
    for arch in ("amd64", "arm64"):
        destination = dist / f"sc_{version}_darwin_{arch}.tar.gz"
        archive(dist / arch, destination)
        checksums[arch] = hashlib.sha256(destination.read_bytes()).hexdigest()
        manifest.append(f"{checksums[arch]}  {destination.name}\n")
    (dist / "checksums.txt").write_text("".join(manifest))
    (dist / "shortcut-cli.rb").write_text(render_cask(version, checksums))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("validate", "package"))
    parser.add_argument("--tag")
    parser.add_argument("--dist", type=Path, default=ROOT / "dist")
    args = parser.parse_args()
    version = validate_version((ROOT / "VERSION").read_text().strip())
    if args.tag is not None and args.tag != "v" + version:
        parser.error(f"tag must match VERSION (v{version})")
    if args.command == "package":
        package(version, args.dist)
    else:
        print(version)


if __name__ == "__main__":
    main()
