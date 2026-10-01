// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Starlight 0.42 ships compiled JS without declarations for its virtual
// modules, so the SocialIcons override needs this one typed by hand.
declare module "virtual:starlight/user-config" {
    const config: import("@astrojs/starlight/types").StarlightConfig;
    export default config;
}
