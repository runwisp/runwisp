#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Build the signed apt and dnf repository served at pkg.runwisp.com from one
# release's packages (packages.sh output). Only that release is listed; the
# site is replaced wholesale on every stable release.
#
# Usage: repo.sh <version> <pkg-dir> <site-dir>
#
# Needs apt-ftparchive (apt-utils), createrepo_c and gpg, with the signing key
# as the default secret key in the gpg keyring.
#
# Layout:
#   runwisp.asc                                 public signing key
#   deb/dists/stable/{InRelease,Release,Release.gpg}
#   deb/dists/stable/main/binary-<arch>/Packages{,.gz}
#   deb/pool/runwisp_<version>_<arch>.deb
#   rpm/runwisp-<version>.<arch>.rpm, rpm/repodata/ (repomd.xml.asc signed)

set -euo pipefail

usage='usage: repo.sh <version> <pkg-dir> <site-dir>'
version=${1:?${usage}}
pkg_dir=${2:?${usage}}
site=${3:?${usage}}
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

rm -rf "${site}"
mkdir -p "${site}/deb/pool" "${site}/rpm"
# The landing page, plus the files the package installs, so adding the
# repository by hand writes the same files to the same paths.
for file in runwisp.asc runwisp.sources runwisp.repo; do
  cp "${here}/${file}" "${site}/${file}"
done
sed "s/@VERSION@/${version}/" "${here}/index.html" >"${site}/index.html"

for target in linux-x64 linux-arm64; do
  case "${target}" in
    linux-x64) deb_arch=amd64 rpm_arch=x86_64 ;;
    linux-arm64) deb_arch=arm64 rpm_arch=aarch64 ;;
  esac
  cp "${pkg_dir}/runwisp-${target}.deb" "${site}/deb/pool/runwisp_${version}_${deb_arch}.deb"
  cp "${pkg_dir}/runwisp-${target}.rpm" "${site}/rpm/runwisp-${version}.${rpm_arch}.rpm"
done

# apt: one suite (stable), one component (main).
(
  cd "${site}/deb"
  for arch in amd64 arm64; do
    dir="dists/stable/main/binary-${arch}"
    mkdir -p "${dir}"
    apt-ftparchive --arch "${arch}" packages pool >"${dir}/Packages"
    gzip -9kn "${dir}/Packages"
  done
  release=$(mktemp)
  apt-ftparchive \
    -o APT::FTPArchive::Release::Origin=RunWisp \
    -o APT::FTPArchive::Release::Label=RunWisp \
    -o APT::FTPArchive::Release::Suite=stable \
    -o APT::FTPArchive::Release::Codename=stable \
    -o APT::FTPArchive::Release::Architectures="amd64 arm64" \
    -o APT::FTPArchive::Release::Components=main \
    release dists/stable >"${release}"
  mv "${release}" dists/stable/Release
  gpg --batch --yes --clearsign -o dists/stable/InRelease dists/stable/Release
  gpg --batch --yes --armor --detach-sign -o dists/stable/Release.gpg dists/stable/Release
)

# dnf: signed repomd.xml (repo_gpgcheck=1 in runwisp.repo).
createrepo_c --quiet "${site}/rpm"
gpg --batch --yes --armor --detach-sign "${site}/rpm/repodata/repomd.xml"
