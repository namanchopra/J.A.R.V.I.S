export namespace agent {
	
	export class AgentInfo {
	    agentType: string;
	    name: string;
	    available: boolean;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agentType = source["agentType"];
	        this.name = source["name"];
	        this.available = source["available"];
	        this.version = source["version"];
	    }
	}

}

export namespace claude {
	
	export class Session {
	    pid: number;
	    sessionId: string;
	    cwd: string;
	    startedAt: number;
	    kind: string;
	    entrypoint: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pid = source["pid"];
	        this.sessionId = source["sessionId"];
	        this.cwd = source["cwd"];
	        this.startedAt = source["startedAt"];
	        this.kind = source["kind"];
	        this.entrypoint = source["entrypoint"];
	        this.name = source["name"];
	    }
	}
	export class SessionIndicator {
	    pid: number;
	    sessionId: string;
	    cwd: string;
	    name: string;
	    startedAt: number;
	    hasQuestion: boolean;
	    lastActivity: string;
	    tokensUsed: number;
	
	    static createFrom(source: any = {}) {
	        return new SessionIndicator(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pid = source["pid"];
	        this.sessionId = source["sessionId"];
	        this.cwd = source["cwd"];
	        this.name = source["name"];
	        this.startedAt = source["startedAt"];
	        this.hasQuestion = source["hasQuestion"];
	        this.lastActivity = source["lastActivity"];
	        this.tokensUsed = source["tokensUsed"];
	    }
	}
	export class SessionUsage {
	    sessionId: string;
	    projectPath: string;
	    // Go type: time
	    startTime: any;
	    durationMinutes: number;
	    inputTokens: number;
	    outputTokens: number;
	    toolCounts: Record<string, number>;
	    messageCount: number;
	    costUsd: number;
	
	    static createFrom(source: any = {}) {
	        return new SessionUsage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.projectPath = source["projectPath"];
	        this.startTime = this.convertValues(source["startTime"], null);
	        this.durationMinutes = source["durationMinutes"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.toolCounts = source["toolCounts"];
	        this.messageCount = source["messageCount"];
	        this.costUsd = source["costUsd"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace cmux {
	
	export class Surface {
	    id: string;
	    ref: string;
	    title: string;
	    tty: string;
	    type: string;
	    pane_ref: string;
	    selected: boolean;
	    selected_in_pane: boolean;
	    focused: boolean;
	    index: number;
	
	    static createFrom(source: any = {}) {
	        return new Surface(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.ref = source["ref"];
	        this.title = source["title"];
	        this.tty = source["tty"];
	        this.type = source["type"];
	        this.pane_ref = source["pane_ref"];
	        this.selected = source["selected"];
	        this.selected_in_pane = source["selected_in_pane"];
	        this.focused = source["focused"];
	        this.index = source["index"];
	    }
	}
	export class Workspace {
	    id: string;
	    ref: string;
	    title: string;
	    current_directory: string;
	    index: number;
	    selected: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Workspace(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.ref = source["ref"];
	        this.title = source["title"];
	        this.current_directory = source["current_directory"];
	        this.index = source["index"];
	        this.selected = source["selected"];
	    }
	}

}

export namespace config {
	
	export class Config {
	    dotClaudeSourcePath: string;
	    defaultAgent: string;
	    scanIntervalSeconds: number;
	    preferredTerminal: string;
	    projectRootPaths: string[];
	    notificationsEnabled: boolean;
	    notifyOnApproval: boolean;
	    notifyOnCompletion: boolean;
	    ciWatchEnabled: boolean;
	    ciProvider: string;
	    defaultCommand: string;
	    mobileAPIPort: number;
	    mobileAPIToken: string;
	    jarvisEnabled: boolean;
	    jarvisProvider: string;
	    jarvisAPIKey: string;
	    jarvisVoice: string;
	    jarvisAmbientEnabled: boolean;
	    jarvisVerbosity: string;
	    jarvisPicovoiceKey: string;
	    jarvisWakeWordModel: string;
	    jarvisWakeSensitivity: number;
	    jarvisElevenLabsKey: string;
	    jarvisElevenLabsVoice: string;
	    useLiveKitTransport: boolean;
	    livekitUrl: string;
	    livekitApiKey: string;
	    livekitApiSecret: string;
	    livekitRoomName: string;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dotClaudeSourcePath = source["dotClaudeSourcePath"];
	        this.defaultAgent = source["defaultAgent"];
	        this.scanIntervalSeconds = source["scanIntervalSeconds"];
	        this.preferredTerminal = source["preferredTerminal"];
	        this.projectRootPaths = source["projectRootPaths"];
	        this.notificationsEnabled = source["notificationsEnabled"];
	        this.notifyOnApproval = source["notifyOnApproval"];
	        this.notifyOnCompletion = source["notifyOnCompletion"];
	        this.ciWatchEnabled = source["ciWatchEnabled"];
	        this.ciProvider = source["ciProvider"];
	        this.defaultCommand = source["defaultCommand"];
	        this.mobileAPIPort = source["mobileAPIPort"];
	        this.mobileAPIToken = source["mobileAPIToken"];
	        this.jarvisEnabled = source["jarvisEnabled"];
	        this.jarvisProvider = source["jarvisProvider"];
	        this.jarvisAPIKey = source["jarvisAPIKey"];
	        this.jarvisVoice = source["jarvisVoice"];
	        this.jarvisAmbientEnabled = source["jarvisAmbientEnabled"];
	        this.jarvisVerbosity = source["jarvisVerbosity"];
	        this.jarvisPicovoiceKey = source["jarvisPicovoiceKey"];
	        this.jarvisWakeWordModel = source["jarvisWakeWordModel"];
	        this.jarvisWakeSensitivity = source["jarvisWakeSensitivity"];
	        this.jarvisElevenLabsKey = source["jarvisElevenLabsKey"];
	        this.jarvisElevenLabsVoice = source["jarvisElevenLabsVoice"];
	        this.useLiveKitTransport = source["useLiveKitTransport"];
	        this.livekitUrl = source["livekitUrl"];
	        this.livekitApiKey = source["livekitApiKey"];
	        this.livekitApiSecret = source["livekitApiSecret"];
	        this.livekitRoomName = source["livekitRoomName"];
	    }
	}

}

export namespace discovery {
	
	export class Repo {
	    name: string;
	    path: string;
	    branch: string;
	    hasAgent: boolean;
	    language: string;
	
	    static createFrom(source: any = {}) {
	        return new Repo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.branch = source["branch"];
	        this.hasAgent = source["hasAgent"];
	        this.language = source["language"];
	    }
	}
	export class Project {
	    name: string;
	    path: string;
	    repos: Repo[];
	    isMonorepo: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Project(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.repos = this.convertValues(source["repos"], Repo);
	        this.isMonorepo = source["isMonorepo"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class RepoSearchResult {
	    name: string;
	    path: string;
	    language: string;
	    hasAgent: boolean;
	    branch: string;
	
	    static createFrom(source: any = {}) {
	        return new RepoSearchResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.language = source["language"];
	        this.hasAgent = source["hasAgent"];
	        this.branch = source["branch"];
	    }
	}
	export class TaskSuggestion {
	    name: string;
	    description: string;
	    repos: string[];
	    prompt: string;
	    sequential: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TaskSuggestion(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.repos = source["repos"];
	        this.prompt = source["prompt"];
	        this.sequential = source["sequential"];
	    }
	}

}

export namespace git {
	
	export class DiffLine {
	    type: string;
	    content: string;
	    oldNum: number;
	    newNum: number;
	
	    static createFrom(source: any = {}) {
	        return new DiffLine(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.content = source["content"];
	        this.oldNum = source["oldNum"];
	        this.newNum = source["newNum"];
	    }
	}
	export class DiffHunk {
	    oldStart: number;
	    oldCount: number;
	    newStart: number;
	    newCount: number;
	    header: string;
	    lines: DiffLine[];
	
	    static createFrom(source: any = {}) {
	        return new DiffHunk(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.oldStart = source["oldStart"];
	        this.oldCount = source["oldCount"];
	        this.newStart = source["newStart"];
	        this.newCount = source["newCount"];
	        this.header = source["header"];
	        this.lines = this.convertValues(source["lines"], DiffLine);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class DiffStats {
	    filesChanged: number;
	    insertions: number;
	    deletions: number;
	
	    static createFrom(source: any = {}) {
	        return new DiffStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.filesChanged = source["filesChanged"];
	        this.insertions = source["insertions"];
	        this.deletions = source["deletions"];
	    }
	}
	export class FileDiff {
	    path: string;
	    oldPath: string;
	    status: string;
	    hunks: DiffHunk[];
	    binary: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FileDiff(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.oldPath = source["oldPath"];
	        this.status = source["status"];
	        this.hunks = this.convertValues(source["hunks"], DiffHunk);
	        this.binary = source["binary"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DiffResult {
	    files: FileDiff[];
	    stats: DiffStats;
	
	    static createFrom(source: any = {}) {
	        return new DiffResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = this.convertValues(source["files"], FileDiff);
	        this.stats = this.convertValues(source["stats"], DiffStats);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class RepoInfo {
	    branch: string;
	    commitCount: number;
	    filesChanged: number;
	    insertions: number;
	    deletions: number;
	    changedFiles: string[];
	    lastCommitMsg: string;
	    lastCommitAge: string;
	    hasUnpushed: boolean;
	    isClean: boolean;
	    remoteUrl: string;
	    prNumber: number;
	
	    static createFrom(source: any = {}) {
	        return new RepoInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.branch = source["branch"];
	        this.commitCount = source["commitCount"];
	        this.filesChanged = source["filesChanged"];
	        this.insertions = source["insertions"];
	        this.deletions = source["deletions"];
	        this.changedFiles = source["changedFiles"];
	        this.lastCommitMsg = source["lastCommitMsg"];
	        this.lastCommitAge = source["lastCommitAge"];
	        this.hasUnpushed = source["hasUnpushed"];
	        this.isClean = source["isClean"];
	        this.remoteUrl = source["remoteUrl"];
	        this.prNumber = source["prNumber"];
	    }
	}
	export class StashEntry {
	    index: number;
	    name: string;
	    date: string;
	
	    static createFrom(source: any = {}) {
	        return new StashEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.name = source["name"];
	        this.date = source["date"];
	    }
	}

}

export namespace impact {
	
	export class ImpactWarning {
	    id: string;
	    severity: string;
	    description: string;
	    sessionA: string;
	    sessionB: string;
	    conflictType: string;
	    details: string;
	
	    static createFrom(source: any = {}) {
	        return new ImpactWarning(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.severity = source["severity"];
	        this.description = source["description"];
	        this.sessionA = source["sessionA"];
	        this.sessionB = source["sessionB"];
	        this.conflictType = source["conflictType"];
	        this.details = source["details"];
	    }
	}

}

export namespace jarvis {
	
	export class JarvisConfig {
	    enabled: boolean;
	    provider: string;
	    apiKey: string;
	    voice: string;
	    ambientEnabled: boolean;
	    verbosity: string;
	    picovoiceAccessKey: string;
	    wakeWordModelPath: string;
	    wakeWordSensitivity: number;
	    elevenLabsKey: string;
	    elevenLabsVoiceId: string;
	
	    static createFrom(source: any = {}) {
	        return new JarvisConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.provider = source["provider"];
	        this.apiKey = source["apiKey"];
	        this.voice = source["voice"];
	        this.ambientEnabled = source["ambientEnabled"];
	        this.verbosity = source["verbosity"];
	        this.picovoiceAccessKey = source["picovoiceAccessKey"];
	        this.wakeWordModelPath = source["wakeWordModelPath"];
	        this.wakeWordSensitivity = source["wakeWordSensitivity"];
	        this.elevenLabsKey = source["elevenLabsKey"];
	        this.elevenLabsVoiceId = source["elevenLabsVoiceId"];
	    }
	}
	export class Message {
	    role: string;
	    content: string;
	    // Go type: time
	    timestamp: any;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	        this.timestamp = this.convertValues(source["timestamp"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace main {
	
	export class MobileConnectionInfo {
	    ips: string[];
	    port: number;
	    token: string;
	
	    static createFrom(source: any = {}) {
	        return new MobileConnectionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ips = source["ips"];
	        this.port = source["port"];
	        this.token = source["token"];
	    }
	}
	export class WorkflowPhase {
	    agentType: string;
	    repoPath: string;
	    prompt: string;
	    phase: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkflowPhase(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agentType = source["agentType"];
	        this.repoPath = source["repoPath"];
	        this.prompt = source["prompt"];
	        this.phase = source["phase"];
	    }
	}
	export class WorkflowSuggestion {
	    name: string;
	    repoDir: string;
	    taskIds: string[];
	
	    static createFrom(source: any = {}) {
	        return new WorkflowSuggestion(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.repoDir = source["repoDir"];
	        this.taskIds = source["taskIds"];
	    }
	}

}

export namespace model {
	
	export class ActivityEvent {
	    id: string;
	    taskId: string;
	    taskName: string;
	    eventType: string;
	    message: string;
	    metadata: string;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new ActivityEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.taskId = source["taskId"];
	        this.taskName = source["taskName"];
	        this.eventType = source["eventType"];
	        this.message = source["message"];
	        this.metadata = source["metadata"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ApprovalRequest {
	    pid: number;
	    sessionName: string;
	    cwd: string;
	    promptText: string;
	    // Go type: time
	    detectedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new ApprovalRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pid = source["pid"];
	        this.sessionName = source["sessionName"];
	        this.cwd = source["cwd"];
	        this.promptText = source["promptText"];
	        this.detectedAt = this.convertValues(source["detectedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DailyCost {
	    date: string;
	    inputTokens: number;
	    outputTokens: number;
	    costUsd: number;
	    sessionCount: number;
	
	    static createFrom(source: any = {}) {
	        return new DailyCost(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.date = source["date"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.costUsd = source["costUsd"];
	        this.sessionCount = source["sessionCount"];
	    }
	}
	export class DashboardStats {
	    total: number;
	    running: number;
	    pending: number;
	    done: number;
	    failed: number;
	    needsInput: number;
	
	    static createFrom(source: any = {}) {
	        return new DashboardStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.running = source["running"];
	        this.pending = source["pending"];
	        this.done = source["done"];
	        this.failed = source["failed"];
	        this.needsInput = source["needsInput"];
	    }
	}
	export class GroupMember {
	    groupId: string;
	    repoPath: string;
	    // Go type: time
	    addedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new GroupMember(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.groupId = source["groupId"];
	        this.repoPath = source["repoPath"];
	        this.addedAt = this.convertValues(source["addedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RecipeStep {
	    id: string;
	    templateId: string;
	    agentType: string;
	    promptTemplate: string;
	    dependsOn: string;
	    sortOrder: number;
	
	    static createFrom(source: any = {}) {
	        return new RecipeStep(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.templateId = source["templateId"];
	        this.agentType = source["agentType"];
	        this.promptTemplate = source["promptTemplate"];
	        this.dependsOn = source["dependsOn"];
	        this.sortOrder = source["sortOrder"];
	    }
	}
	export class Session {
	    id: string;
	    taskId: string;
	    agentType: string;
	    repoPath: string;
	    prompt: string;
	    agentSessionId: string;
	    status: string;
	    pid: number;
	    outputPath: string;
	    exitCode: number;
	    errorMessage: string;
	    parentSessionId: string;
	    dependsOn: string[];
	    phase: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.taskId = source["taskId"];
	        this.agentType = source["agentType"];
	        this.repoPath = source["repoPath"];
	        this.prompt = source["prompt"];
	        this.agentSessionId = source["agentSessionId"];
	        this.status = source["status"];
	        this.pid = source["pid"];
	        this.outputPath = source["outputPath"];
	        this.exitCode = source["exitCode"];
	        this.errorMessage = source["errorMessage"];
	        this.parentSessionId = source["parentSessionId"];
	        this.dependsOn = source["dependsOn"];
	        this.phase = source["phase"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SessionGroup {
	    id: string;
	    name: string;
	    description: string;
	    color: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new SessionGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.color = source["color"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SessionTemplate {
	    id: string;
	    name: string;
	    agentType: string;
	    repoPaths: string[];
	    command: string;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new SessionTemplate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.agentType = source["agentType"];
	        this.repoPaths = source["repoPaths"];
	        this.command = source["command"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SessionTodo {
	    id: string;
	    sessionId: string;
	    title: string;
	    status: string;
	    sortOrder: number;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new SessionTodo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.title = source["title"];
	        this.status = source["status"];
	        this.sortOrder = source["sortOrder"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Task {
	    id: string;
	    name: string;
	    description: string;
	    repoPath: string;
	    agentType: string;
	    status: string;
	    outputPath: string;
	    workflowId: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Task(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.repoPath = source["repoPath"];
	        this.agentType = source["agentType"];
	        this.status = source["status"];
	        this.outputPath = source["outputPath"];
	        this.workflowId = source["workflowId"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TemplateParam {
	    id: string;
	    templateId: string;
	    name: string;
	    paramType: string;
	    defaultValue: string;
	    description: string;
	    sortOrder: number;
	
	    static createFrom(source: any = {}) {
	        return new TemplateParam(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.templateId = source["templateId"];
	        this.name = source["name"];
	        this.paramType = source["paramType"];
	        this.defaultValue = source["defaultValue"];
	        this.description = source["description"];
	        this.sortOrder = source["sortOrder"];
	    }
	}
	export class TotalSpend {
	    allTime: number;
	    thisMonth: number;
	    today: number;
	
	    static createFrom(source: any = {}) {
	        return new TotalSpend(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.allTime = source["allTime"];
	        this.thisMonth = source["thisMonth"];
	        this.today = source["today"];
	    }
	}
	export class Workflow {
	    id: string;
	    name: string;
	    description: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Workflow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace nlquery {
	
	export class QueryResult {
	    action: string;
	    intent: string;
	    data: any;
	    needsConfirm: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new QueryResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.action = source["action"];
	        this.intent = source["intent"];
	        this.data = source["data"];
	        this.needsConfirm = source["needsConfirm"];
	        this.error = source["error"];
	    }
	}

}

export namespace recording {
	
	export class RecordingSummary {
	    sessionId: string;
	    name: string;
	    cwd: string;
	    // Go type: time
	    startedAt: any;
	    snapshotCount: number;
	    filePath: string;
	
	    static createFrom(source: any = {}) {
	        return new RecordingSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.name = source["name"];
	        this.cwd = source["cwd"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.snapshotCount = source["snapshotCount"];
	        this.filePath = source["filePath"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Snapshot {
	    // Go type: time
	    timestamp: any;
	    pid: number;
	    sessionId: string;
	    cwd: string;
	    terminalText: string;
	    activity: string;
	    toolCalls: string[];
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.timestamp = this.convertValues(source["timestamp"], null);
	        this.pid = source["pid"];
	        this.sessionId = source["sessionId"];
	        this.cwd = source["cwd"];
	        this.terminalText = source["terminalText"];
	        this.activity = source["activity"];
	        this.toolCalls = source["toolCalls"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace store {
	
	export class OutputSearchResult {
	    taskId: string;
	    taskName: string;
	    line: string;
	    lineNum: number;
	
	    static createFrom(source: any = {}) {
	        return new OutputSearchResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.taskId = source["taskId"];
	        this.taskName = source["taskName"];
	        this.line = source["line"];
	        this.lineNum = source["lineNum"];
	    }
	}
	export class Project {
	    id: string;
	    name: string;
	    path: string;
	    isMonorepo: boolean;
	    repoPaths: string[];
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Project(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.isMonorepo = source["isMonorepo"];
	        this.repoPaths = source["repoPaths"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}

}

export namespace terminal {
	
	export class TerminalWindow {
	    id: string;
	    name: string;
	    cwd: string;
	    tty: string;
	    app: string;
	    selected: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TerminalWindow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.cwd = source["cwd"];
	        this.tty = source["tty"];
	        this.app = source["app"];
	        this.selected = source["selected"];
	    }
	}

}

export namespace workspace {
	
	export class Workspace {
	    id: string;
	    name: string;
	    path: string;
	    repoPaths: string[];
	    prompt: string;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Workspace(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.repoPaths = source["repoPaths"];
	        this.prompt = source["prompt"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

