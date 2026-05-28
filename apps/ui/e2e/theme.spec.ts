// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { test, expect, type Page, type Locator } from "./fixtures/test-base";

// Relative sRGB luminance (0 = black, 1 = white) of a computed CSS property.
// Painting to a 1x1 canvas lets the browser resolve any color format — the
// theme is authored in oklch(), which a naive rgb() parser would mangle — so
// this measures what the user actually sees and catches a toggle that flips a
// class but leaves text/surface colors unchanged.
async function luminanceOf(
    locator: Locator,
    property: "color" | "background-color" | "stroke",
): Promise<number> {
    return locator.evaluate((el, prop) => {
        const color = getComputedStyle(el).getPropertyValue(prop);
        const canvas = document.createElement("canvas");
        canvas.width = 1;
        canvas.height = 1;
        const ctx = canvas.getContext("2d");
        if (!ctx) throw new Error("no 2d context");
        ctx.fillStyle = color;
        ctx.fillRect(0, 0, 1, 1);
        const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;
        return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
    }, property);
}

const html = (page: Page) => page.locator("html");
const surfaceLuminance = (page: Page) => luminanceOf(page.locator("body"), "background-color");

async function openThemeMenu(page: Page): Promise<void> {
    await page.getByRole("button", { name: "Theme" }).click();
}

async function pickTheme(page: Page, name: "Auto" | "Light" | "Dark"): Promise<void> {
    await openThemeMenu(page);
    await page.getByRole("menuitem", { name }).click();
}

test.describe("theme switch", () => {
    test.beforeEach(async ({ authenticatedPage: page }) => {
        // Each test gets an isolated context (localStorage starts empty), so the
        // only baseline we need is a known OS preference.
        await page.emulateMedia({ colorScheme: "light" });
    });

    test("defaults to auto and follows a light OS preference", async ({
        authenticatedPage: page,
    }) => {
        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();
        await expect(html(page)).not.toHaveClass(/dark/);
    });

    test("selecting Dark applies .dark and actually darkens the surface", async ({
        authenticatedPage: page,
    }) => {
        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

        const lightLum = await surfaceLuminance(page);

        await pickTheme(page, "Dark");

        await expect(html(page)).toHaveClass(/dark/);
        await expect.poll(() => surfaceLuminance(page)).toBeLessThan(0.5);
        expect(await surfaceLuminance(page)).toBeLessThan(lightLum);

        // A migrated heading must now render light text (proves the raw palette →
        // semantic token migration, not just the class toggle). Poll because these
        // elements animate via `transition-colors`, so the color settles over ~200ms.
        const heading = page.getByRole("heading", { name: "Tasks" });
        await expect.poll(() => luminanceOf(heading, "color")).toBeGreaterThan(0.5);
    });

    test("choice persists across reload", async ({ authenticatedPage: page }) => {
        await page.goto("/");
        await pickTheme(page, "Dark");
        await expect(html(page)).toHaveClass(/dark/);

        await page.reload();
        // .dark is present immediately (the inline app.html script applies it
        // before SvelteKit hydrates — no flash of the wrong theme).
        await expect(html(page)).toHaveClass(/dark/);
    });

    test("selecting Light removes .dark and restores the light surface", async ({
        authenticatedPage: page,
    }) => {
        await page.goto("/");
        const lightLum = await surfaceLuminance(page);

        await pickTheme(page, "Dark");
        await expect(html(page)).toHaveClass(/dark/);

        await pickTheme(page, "Light");
        await expect(html(page)).not.toHaveClass(/dark/);
        await expect.poll(() => surfaceLuminance(page)).toBeCloseTo(lightLum, 1);
    });

    test("CPU/Memory sparklines flip stroke color and stay visible in both modes", async ({
        authenticatedPage: page,
    }) => {
        // Regression: the dashboard sparklines used to hardcode `#1e293b` (CPU)
        // and `#0284c7` (Memory), which left the CPU line near-invisible against
        // the dark surface. They now use semantic tokens via `currentColor`, so
        // both stroke and surface flip together — assert real contrast in both
        // themes, not just that a class is present.
        await page.goto("/");
        await expect(page.getByRole("heading", { name: "System resources" })).toBeVisible();

        // The CPU wrapper carries `text-primary`; Memory carries `text-info`.
        // `fill="none"` picks the stroke path (vs. the filled area underneath).
        const cpuStroke = page.locator('div.text-primary svg path[fill="none"]').first();
        const memStroke = page.locator('div.text-info svg path[fill="none"]').first();
        await expect(cpuStroke).toBeVisible();
        await expect(memStroke).toBeVisible();

        const lightSurface = await surfaceLuminance(page);
        const cpuLight = await luminanceOf(cpuStroke, "stroke");
        const memLight = await luminanceOf(memStroke, "stroke");
        // Visible contrast against the surface in light mode.
        expect(Math.abs(cpuLight - lightSurface)).toBeGreaterThan(0.1);
        expect(Math.abs(memLight - lightSurface)).toBeGreaterThan(0.1);

        await pickTheme(page, "Dark");
        await expect(html(page)).toHaveClass(/dark/);
        await expect.poll(() => surfaceLuminance(page)).toBeLessThan(0.5);

        // Stroke colors must actually change (not just the class) and still
        // contrast with the now-dark surface.
        await expect.poll(() => luminanceOf(cpuStroke, "stroke")).not.toBeCloseTo(cpuLight, 1);
        const darkSurface = await surfaceLuminance(page);
        const cpuDark = await luminanceOf(cpuStroke, "stroke");
        const memDark = await luminanceOf(memStroke, "stroke");
        expect(Math.abs(cpuDark - darkSurface)).toBeGreaterThan(0.1);
        expect(Math.abs(memDark - darkSurface)).toBeGreaterThan(0.1);
    });

    test("auto tracks live OS preference changes", async ({ authenticatedPage: page }) => {
        await page.goto("/");
        await pickTheme(page, "Auto");
        await expect(html(page)).not.toHaveClass(/dark/);

        await page.emulateMedia({ colorScheme: "dark" });
        await expect(html(page)).toHaveClass(/dark/);

        await page.emulateMedia({ colorScheme: "light" });
        await expect(html(page)).not.toHaveClass(/dark/);
    });
});
