#!/usr/bin/env python3
"""Generate the tf.lacyhq.name provider registry JSON documents.

Reads release assets from the aryn-lacy/Cosmos-Server repo and writes the
registry documents into a target directory (committed to the branch that
GitHub Pages serves):

  /.well-known/terraform.json                          -> discovery
  /v1/providers/aryn/cosmos/versions                   -> version listing
  /v1/providers/aryn/cosmos/<v>/download/<os>/<arch>   -> download response

Download responses point at GitHub release assets. Lock-file h1: hashes are
NOT computed here — `terraform init` computes them when it first pulls from
this registry and writes them to .terraform.lock.hcl (commit that back).
"""
import json
import os
import subprocess
import sys

REPO = "aryn-lacy/Cosmos-Server"
NAMESPACE, TYPE = "aryn", "cosmos"


def gh_json(path):
    out = subprocess.run(["gh", "api", path], capture_output=True, text=True, check=True)
    return json.loads(out.stdout)


def main():
    outdir = sys.argv[1] if len(sys.argv) > 1 else "registry-out"
    releases = gh_json(f"repos/{REPO}/releases?per_page=100")
    versions = {}
    for rel in releases:
        if rel.get("draft") or rel.get("prerelease"):
            continue
        tag = rel["tag_name"]
        if not tag.startswith("v"):
            continue
        ver = tag[1:]
        platforms = []
        for asset in rel["assets"]:
            name = asset["name"]
            if not (name.startswith("terraform-provider-cosmos_") and name.endswith(".zip")):
                continue
            # terraform-provider-cosmos_0.22.35-lacy.1_darwin-amd64.zip
            # NOTE: version strings contain hyphens, so parse only the LAST
            # underscore-separated token for os-arch (hyphen-joined).
            stem = name[: -len(".zip")]
            try:
                _prefix, platform = stem.rsplit("_", 1)
                os_name, arch = platform.split("-", 1)
            except ValueError:
                continue
            platforms.append({
                "os": os_name,
                "arch": arch,
                "url": asset["browser_download_url"],
                "asset": name,
            })
        if platforms:
            versions[ver] = {"tag": tag, "platforms": platforms}

    if not versions:
        sys.exit("no suitable releases found")

    providers_dir = os.path.join(outdir, "v1", "providers", NAMESPACE, TYPE)
    os.makedirs(os.path.join(outdir, ".well-known"), exist_ok=True)
    os.makedirs(providers_dir, exist_ok=True)

    # discovery
    with open(os.path.join(outdir, ".well-known", "terraform.json"), "w") as f:
        json.dump({"providers.v1": "/v1/providers/"}, f, indent=2)

    # versions listing
    versions_doc = {
        "versions": [
            {
                "version": ver,
                "protocols": ["5.0", "6.0"],
                "platforms": [{"os": p["os"], "arch": p["arch"]} for p in info["platforms"]],
            }
            for ver, info in sorted(versions.items(), reverse=True)
        ]
    }
    with open(os.path.join(providers_dir, "versions"), "w") as f:
        json.dump(versions_doc, f, indent=2)

    # download documents
    for ver, info in versions.items():
        for p in info["platforms"]:
            ddir = os.path.join(providers_dir, ver, "download", p["os"], p["arch"])
            os.makedirs(ddir, exist_ok=True)
            doc = {
                "protocols": ["5.0", "6.0"],
                "os": p["os"],
                "arch": p["arch"],
                "filename": p["asset"],
                "download_url": p["url"],
            }
            with open(os.path.join(ddir, "index.html"), "w") as f:
                json.dump(doc, f, indent=2)

    # GitHub Pages runs Jekyll by default: dot-directories (.well-known) and
    # extensionless files can be dropped. .nojekyll disables that so the
    # whole tree is served verbatim as static files.
    with open(os.path.join(outdir, ".nojekyll"), "w") as f:
        f.write("")

    print(f"wrote registry docs for versions: {', '.join(sorted(versions, reverse=True))}")
    for ver, info in versions.items():
        for p in info["platforms"]:
            print(f"  {ver} {p['os']}/{p['arch']} -> {p['url']}")


if __name__ == "__main__":
    main()
