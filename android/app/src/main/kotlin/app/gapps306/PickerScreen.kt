package app.gapps306

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExposedDropdownMenuAnchorType
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TriStateCheckbox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.state.ToggleableState
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PickerScreen(
    onBuild: (name: String) -> Unit,
    onSave: (BuildResult) -> Unit,
    onShare: (BuildResult) -> Unit,
    onInstallUpdate: () -> Unit = {},
) {
    val picker by Gapps.picker.collectAsStateWithLifecycle()
    val build by Gapps.build.collectAsStateWithLifecycle()
    val update by Updater.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    val ctx = LocalContext.current
    var naming by rememberSaveable { mutableStateOf(false) }

    Scaffold(
        topBar = { TopAppBar(title = { Text("306Gapps") }) },
        bottomBar = {
            BottomBar(picker, building = build is BuildStatus.Running, onBuild = { naming = true })
        },
    ) { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            UpdateBanner(update, onInstall = onInstallUpdate,
                onDownload = { scope.launch { Updater.download(ctx) } })
            if (picker.releases.isNotEmpty()) {
                ReleasePicker(picker) { scope.launch { Gapps.loadRelease(it) } }
            }
            val cat = picker.catalog
            val error = picker.error
            when {
                picker.loading -> Box(Modifier.fillMaxSize(), Alignment.Center) {
                    CircularProgressIndicator()
                }
                error != null -> ErrorPane(error) {
                    scope.launch {
                        if (cat == null) Gapps.loadReleases() else Gapps.loadRelease(cat.release.id)
                    }
                }
                cat != null -> {
                    Presets(cat, picker.selection) { scope.launch { Gapps.applyVariant(it) } }
                    PackageList(cat, picker.selection, scope)
                }
            }
        }
    }

    if (naming) {
        NameDialog(
            default = remember { Gapps.zipName() },
            onDismiss = { naming = false },
            onBuild = { naming = false; onBuild(it) },
        )
    }
    BuildDialog(build, onSave, onShare)
}

@Composable
private fun NameDialog(default: String, onDismiss: () -> Unit, onBuild: (String) -> Unit) {
    // select the stem so typing replaces it and .zip stays
    var field by remember {
        mutableStateOf(TextFieldValue(default, TextRange(0, default.removeSuffix(".zip").length)))
    }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { focus.requestFocus() }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Build recovery zip") },
        text = {
            OutlinedTextField(
                value = field,
                onValueChange = { field = it },
                label = { Text("File name") },
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
                keyboardActions = KeyboardActions(onDone = { onBuild(field.text) }),
                modifier = Modifier.fillMaxWidth().focusRequester(focus),
            )
        },
        confirmButton = { TextButton(onClick = { onBuild(field.text) }) { Text("Build") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ReleasePicker(picker: Picker, onPick: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val current = picker.catalog?.release
    ExposedDropdownMenuBox(
        expanded = open,
        onExpandedChange = { open = it },
        modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
    ) {
        OutlinedTextField(
            value = current?.label ?: "Select a release",
            onValueChange = {},
            readOnly = true,
            label = { Text("Release") },
            trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(open) },
            modifier = Modifier
                .menuAnchor(ExposedDropdownMenuAnchorType.PrimaryNotEditable)
                .fillMaxWidth(),
        )
        ExposedDropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            picker.releases.forEach { r ->
                DropdownMenuItem(
                    text = {
                        Column {
                            Text("Android ${r.version}")
                            Text(r.id, style = MaterialTheme.typography.bodySmall)
                        }
                    },
                    onClick = {
                        open = false
                        if (r.id != current?.id) onPick(r.id)
                    },
                )
            }
        }
    }
}

@Composable
private fun Presets(cat: Catalog, sel: Selection, onPick: (String) -> Unit) {
    if (cat.variants.isEmpty()) return
    Row(
        Modifier
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = 16.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        cat.variants.forEach { v ->
            FilterChip(
                selected = sel.variant == v.id,
                onClick = { onPick(v.id) },
                label = { Text(v.name) },
            )
        }
    }
}

@Composable
private fun PackageList(cat: Catalog, sel: Selection, scope: kotlinx.coroutines.CoroutineScope) {
    val names = remember(cat) { cat.names() }
    LazyColumn(Modifier.fillMaxSize()) {
        cat.groups.forEach { g ->
            item(key = "g:" + g.id) {
                GroupHeader(g, groupCheck(g, sel)) { on -> scope.launch { Gapps.setGroup(g.id, on) } }
            }
            items(g.packages, key = { "p:" + it.id }) { p ->
                PackageRow(p, sel, names) { on -> scope.launch { Gapps.toggle(p.id, on) } }
            }
        }
    }
}

@Composable
private fun GroupHeader(g: Group, check: GroupCheck, onSet: (Boolean) -> Unit) {
    val state = when (check) {
        GroupCheck.ALL -> ToggleableState.On
        GroupCheck.SOME -> ToggleableState.Indeterminate
        GroupCheck.NONE -> ToggleableState.Off
    }
    val selectable = g.packages.any { !it.required }
    Column {
        HorizontalDivider()
        Row(
            Modifier.fillMaxWidth().padding(start = 4.dp, end = 16.dp, top = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            TriStateCheckbox(
                state = state,
                enabled = selectable,
                onClick = { onSet(check != GroupCheck.ALL) },
            )
            Column(Modifier.weight(1f)) {
                Text(
                    g.name.uppercase(),
                    style = MaterialTheme.typography.labelLarge,
                    color = MaterialTheme.colorScheme.primary,
                )
                if (g.summary.isNotEmpty()) {
                    Text(g.summary, style = MaterialTheme.typography.bodySmall, maxLines = 2,
                        overflow = TextOverflow.Ellipsis)
                }
            }
            Text(humanSize(g.size), style = MaterialTheme.typography.labelMedium)
        }
    }
}

@Composable
private fun PackageRow(p: Package, sel: Selection, names: Map<String, String>, onSet: (Boolean) -> Unit) {
    var expanded by rememberSaveable(p.id) { mutableStateOf(false) }
    val on = p.id in sel.set
    val implied = p.id in sel.implied
    Row(
        Modifier
            .fillMaxWidth()
            .clickable(enabled = p.summary.isNotEmpty()) { expanded = !expanded }
            .padding(start = 4.dp, end = 16.dp, top = 2.dp, bottom = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Checkbox(checked = on, enabled = !p.required, onCheckedChange = onSet)
        Column(Modifier.weight(1f)) {
            Text(p.name, style = MaterialTheme.typography.bodyLarge)
            val note = if (expanded && !p.required && !implied) p.summary else packageNote(p, sel, names)
            if (note.isNotEmpty()) {
                Text(
                    note,
                    style = MaterialTheme.typography.bodySmall,
                    color = if (implied) MaterialTheme.colorScheme.tertiary
                    else MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = if (expanded) Int.MAX_VALUE else 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        Spacer(Modifier.width(8.dp))
        Text(humanSize(p.size), style = MaterialTheme.typography.labelMedium)
    }
}

@Composable
private fun BottomBar(picker: Picker, building: Boolean, onBuild: () -> Unit) {
    val sel = picker.selection
    Surface(tonalElevation = 3.dp) {
        Column(Modifier.fillMaxWidth().navigationBarsPadding().padding(16.dp)) {
            if (sel.error.isNotEmpty()) {
                Text(sel.error, color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall)
                Spacer(Modifier.height(8.dp))
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    if (picker.catalog != null && sel.error.isEmpty()) {
                        Text("${sel.count} packages · ${humanSize(sel.size)}",
                            style = MaterialTheme.typography.bodyMedium)
                        val deps = sel.implied.size
                        if (deps > 0) {
                            Text("$deps pulled in as dependencies",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                }
                Button(
                    onClick = onBuild,
                    enabled = picker.catalog != null && !picker.loading &&
                        sel.error.isEmpty() && !building,
                ) { Text("Build recovery zip") }
            }
        }
    }
}

@Composable
private fun ErrorPane(message: String, onRetry: () -> Unit) {
    Column(
        Modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(message, color = MaterialTheme.colorScheme.error)
        Spacer(Modifier.height(16.dp))
        Button(onClick = onRetry) { Text("Retry") }
    }
}

@Composable
private fun BuildDialog(
    build: BuildStatus,
    onSave: (BuildResult) -> Unit,
    onShare: (BuildResult) -> Unit,
) {
    when (build) {
        BuildStatus.Idle -> Unit
        is BuildStatus.Running -> AlertDialog(
            onDismissRequest = {},
            title = { Text("Building") },
            text = {
                Column {
                    Text(build.line)
                    Spacer(Modifier.height(12.dp))
                    LinearProgressIndicator(progress = { build.frac }, Modifier.fillMaxWidth())
                    Spacer(Modifier.height(12.dp))
                    Text(
                        "Keeps going if you leave the app.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            },
            confirmButton = {},
            dismissButton = { TextButton(onClick = Gapps::cancelBuild) { Text("Cancel") } },
        )
        is BuildStatus.Done -> {
            val r = build.result
            AlertDialog(
                onDismissRequest = Gapps::dismissBuild,
                title = { Text("Package ready") },
                text = {
                    SelectionContainer {
                        Column {
                            Text(r.name, fontWeight = FontWeight.Bold)
                            Text("${humanSize(r.size)} · ${r.files} entries",
                                style = MaterialTheme.typography.bodySmall)
                            Spacer(Modifier.height(8.dp))
                            Text("sha256", style = MaterialTheme.typography.labelSmall)
                            Text(r.sha256, fontFamily = FontFamily.Monospace,
                                style = MaterialTheme.typography.bodySmall)
                            Spacer(Modifier.height(8.dp))
                            Text("Save it somewhere recovery can reach, then flash it.",
                                style = MaterialTheme.typography.bodySmall)
                        }
                    }
                },
                confirmButton = { TextButton(onClick = { onSave(r) }) { Text("Save…") } },
                dismissButton = {
                    Row {
                        TextButton(onClick = { onShare(r) }) { Text("Share") }
                        TextButton(onClick = Gapps::dismissBuild) { Text("Close") }
                    }
                },
            )
        }
        is BuildStatus.Failed -> AlertDialog(
            onDismissRequest = Gapps::dismissBuild,
            title = { Text("Build failed") },
            text = { SelectionContainer { Text(build.message) } },
            confirmButton = { TextButton(onClick = Gapps::dismissBuild) { Text("OK") } },
        )
    }
}


/**
 * A newer release, offered above the picker.
 *
 * Deliberately a strip rather than a dialog: an update is never more urgent
 * than what the user opened the app to do.
 */
@Composable
private fun UpdateBanner(state: UpdateState, onDownload: () -> Unit, onInstall: () -> Unit) {
    if (state is UpdateState.None) return
    Surface(color = MaterialTheme.colorScheme.secondaryContainer) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(Modifier.weight(1f)) {
                when (state) {
                    is UpdateState.Ready ->
                        Text("${state.update.version} is available", style = MaterialTheme.typography.bodyMedium)
                    is UpdateState.Downloading -> {
                        Text("Downloading ${state.update.version}…",
                            style = MaterialTheme.typography.bodyMedium)
                        LinearProgressIndicator(
                            progress = { state.frac },
                            modifier = Modifier.fillMaxWidth().padding(top = 6.dp),
                        )
                    }
                    is UpdateState.Downloaded ->
                        Text("${state.update.version} is ready to install",
                            style = MaterialTheme.typography.bodyMedium)
                    is UpdateState.Failed ->
                        Text("Could not download ${state.update.version}: ${state.message}",
                            style = MaterialTheme.typography.bodyMedium)
                    UpdateState.None -> Unit
                }
            }
            when (state) {
                is UpdateState.Ready -> TextButton(onClick = onDownload) { Text("Download") }
                is UpdateState.Downloaded -> TextButton(onClick = onInstall) { Text("Install") }
                is UpdateState.Failed -> TextButton(onClick = onDownload) { Text("Retry") }
                else -> Unit
            }
            if (state !is UpdateState.Downloading) {
                TextButton(onClick = { Updater.dismiss() }) { Text("Later") }
            }
        }
    }
}
