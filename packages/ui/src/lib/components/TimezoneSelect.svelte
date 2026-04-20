<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import Select from "./Select.svelte";
    import { onMount } from "svelte";
    import { type Component } from "svelte";

    let {
        value = $bindable(),
        label = "Timezone",
        disabled = false,
        hint,
    } = $props<{
        value: string;
        label?: string;
        disabled?: boolean;
        hint?: string;
    }>();

    type ZoneOption = {
        value: string;
        label: string;
        group?: string | undefined;
        description?: string | undefined;
        icon?: Component;
    };
    let options = $state<ZoneOption[]>([]);

    function getOffset(zone: string): string {
        try {
            const formatter = new Intl.DateTimeFormat("en-US", {
                timeZone: zone,
                timeZoneName: "shortOffset",
            });
            const parts = formatter.formatToParts(new Date());
            const offset = parts.find((p) => p.type === "timeZoneName")?.value;
            return offset?.replace("GMT", "UTC") || "";
        } catch {
            return "";
        }
    }

    function toRegion(zone: string): string {
        const separatorIndex = zone.indexOf("/");
        return separatorIndex === -1 ? "Other" : zone.slice(0, separatorIndex);
    }

    function toPrettyLabel(zone: string): string {
        return zone.split("/").slice(1).join(" / ").replace(/_/g, " ");
    }

    function toZoneOption(zone: string): ZoneOption {
        const offset = getOffset(zone);
        return {
            value: zone,
            label: toPrettyLabel(zone),
            group: toRegion(zone),
            description: offset ? `Timezone offset: ${offset}` : undefined,
        };
    }

    function buildFixedOffsetOptions(): ZoneOption[] {
        const fixedOffsets: ZoneOption[] = [];

        for (let offsetHours = -12; offsetHours <= 14; offsetHours++) {
            const signDisplay = offsetHours >= 0 ? "+" : "-";
            const abs = Math.abs(offsetHours).toString().padStart(2, "0");
            const label = `UTC${signDisplay}${abs}:00`;
            const value =
                offsetHours === 0
                    ? "UTC"
                    : `Etc/GMT${offsetHours > 0 ? "-" : "+"}${Math.abs(offsetHours)}`;

            fixedOffsets.push({
                value,
                label,
                group: "Fixed Offsets",
                description: "Fixed constant time offset",
            });
        }

        return fixedOffsets;
    }

    function buildTimezoneOptions(): ZoneOption[] {
        const regions: Record<string, ZoneOption[]> = {};

        for (const zone of Intl.supportedValuesOf("timeZone")) {
            if (zone.startsWith("Etc/")) continue;

            const option = toZoneOption(zone);
            const region = option.group ?? "Other";
            regions[region] ??= [];
            regions[region].push(option);
        }

        const regionalOptions = Object.keys(regions)
            .sort()
            .flatMap((region) => regions[region]!.sort((a, b) => a.label.localeCompare(b.label)));

        return [...buildFixedOffsetOptions(), ...regionalOptions];
    }

    onMount(() => {
        try {
            options = buildTimezoneOptions();

            if (!value) {
                value = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
            }
        } catch (e) {
            console.warn("Timezone load failed", e);
            options = [{ value: "UTC", label: "UTC", description: "Universal Coordinated Time" }];
        }
    });
</script>

<Select
    {label}
    bind:value
    {disabled}
    {hint}
    placeholder="Select timezone..."
    {options}
    searchable={true}
></Select>
