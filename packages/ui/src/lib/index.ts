// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export { default as theme } from "./theme.css?inline";

export { default as Logo } from "./components/Logo.svelte";

export { default as PageContainer } from "./components/PageContainer.svelte";
export { default as PageHeader } from "./components/PageHeader.svelte";

export { default as Heading } from "./components/Heading.svelte";
export { default as Prose } from "./components/Prose.svelte";
export { default as Text } from "./components/Text.svelte";

export { default as Kbd } from "./components/Kbd.svelte";
export { default as Link } from "./components/Link.svelte";
export { default as Separator } from "./components/Separator.svelte";
export { default as Spinner } from "./components/Spinner.svelte";

export { default as Button } from "./components/Button.svelte";
export { default as LinkButton } from "./components/LinkButton.svelte";
export {
    BUTTON_BASE,
    BUTTON_SIZES,
    BUTTON_VARIANTS,
    type ButtonSize,
    type ButtonVariant,
} from "./components/button-styles.js";
export { default as Checkbox } from "./components/Checkbox.svelte";
export { default as CronInput } from "./components/CronInput.svelte";
export { default as DurationInput } from "./components/DurationInput.svelte";
export { default as FormField } from "./components/FormField.svelte";
export { default as Input } from "./components/Input.svelte";
export { default as Radio } from "./components/Radio.svelte";
export { default as RadioGroup } from "./components/RadioGroup.svelte";
export { default as Select } from "./components/Select.svelte";
export { default as Textarea } from "./components/Textarea.svelte";
export { default as TimezoneSelect } from "./components/TimezoneSelect.svelte";
export { default as Toggle } from "./components/Toggle.svelte";

export { default as Accordion } from "./components/Accordion.svelte";
export { default as AccordionItem } from "./components/AccordionItem.svelte";
export { default as Breadcrumb } from "./components/Breadcrumb.svelte";
export { default as Pagination } from "./components/Pagination.svelte";
export { default as Tabs } from "./components/Tabs.svelte";
export { default as ThemeToggle } from "./components/ThemeToggle.svelte";

export { default as Avatar } from "./components/Avatar.svelte";
export { default as Badge } from "./components/Badge.svelte";
export { default as Card } from "./components/Card.svelte";
export { default as DataGrid } from "./components/DataGrid.svelte";
export { default as Progress } from "./components/Progress.svelte";
export { default as Skeleton } from "./components/Skeleton.svelte";
export { default as Sparkline } from "./components/Sparkline.svelte";
export { default as StatusIndicator } from "./components/StatusIndicator.svelte";
export { default as Table } from "./components/Table.svelte";
export { default as TableBody } from "./components/TableBody.svelte";
export { default as TableCell } from "./components/TableCell.svelte";
export { default as TableHead } from "./components/TableHead.svelte";
export { default as TableRow } from "./components/TableRow.svelte";

export { default as Alert } from "./components/Alert.svelte";
export { default as AlertDialog } from "./components/AlertDialog.svelte";
export { default as Drawer } from "./components/Drawer.svelte";
export { default as Dropdown } from "./components/Dropdown.svelte";
export { default as EmptyState } from "./components/EmptyState.svelte";
export { default as ErrorState } from "./components/ErrorState.svelte";
export { default as Modal } from "./components/Modal.svelte";
export { default as Popover } from "./components/Popover.svelte";
export { default as ToastContainer } from "./components/ToastContainer.svelte";
export { default as Tooltip } from "./components/Tooltip.svelte";

export { default as CodeBlock } from "./components/CodeBlock.svelte";
export { default as FeatureCard } from "./components/FeatureCard.svelte";
export { default as FeatureGrid } from "./components/FeatureGrid.svelte";
export { default as TaskCard } from "./components/TaskCard.svelte";
export type { TaskCardAccent } from "./components/TaskCard.svelte";

export { default as LogConsole } from "./components/LogConsole.svelte";
export type { FetchLogsFn, LogEvent, LogSlice } from "./log-console/types.js";
export { isLogEvent } from "./log-console/types.js";
export { LogCache } from "./log-console/LogCache.svelte.js";
export { LogFetcher } from "./log-console/LogFetcher.svelte.js";

export { default as RunDetailPanel } from "./components/dashboard/RunDetailPanel.svelte";
export { default as RunsList } from "./components/dashboard/RunsList.svelte";
export {
    getRunStatusConfig,
    runDisplayStatus,
    RUN_STATUS_CONFIG,
    type RunStatusConfig,
} from "./components/dashboard/status-config.js";
export { runDuration } from "./components/dashboard/run-helpers.js";
export type { DaemonState, DaemonStats } from "./components/dashboard/types.js";

export { toast, type Toast, type ToastType } from "./utils/toast.svelte.js";
export { extractErrorMessage } from "./utils/error.js";
export {
    formatBytes,
    formatRelativeTime,
    formatRelativeTimeWithAbsolute,
    formatDateTime,
    formatDuration,
    formatFullDateTime,
} from "./utils/format.js";
export { formatShortId } from "./utils/id.js";
