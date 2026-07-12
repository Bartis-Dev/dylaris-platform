export namespace main {
	
	export class BeamBranding {
	    name: string;
	    logo_url: string;
	
	    static createFrom(source: any = {}) {
	        return new BeamBranding(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.logo_url = source["logo_url"];
	    }
	}
	export class BeamConfig {
	    relay_address: string;
	    enabled: boolean;
	    branding: BeamBranding;
	
	    static createFrom(source: any = {}) {
	        return new BeamConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.relay_address = source["relay_address"];
	        this.enabled = source["enabled"];
	        this.branding = this.convertValues(source["branding"], BeamBranding);
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
	export class BeamServer {
	    id: number;
	    uuid: string;
	    name: string;
	    status: string;
	    node_id: string;
	    node_name: string;
	    active_sub_server: string;
	
	    static createFrom(source: any = {}) {
	        return new BeamServer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.uuid = source["uuid"];
	        this.name = source["name"];
	        this.status = source["status"];
	        this.node_id = source["node_id"];
	        this.node_name = source["node_name"];
	        this.active_sub_server = source["active_sub_server"];
	    }
	}
	export class FileContentResult {
	    success: boolean;
	    content: string;
	    message?: string;
	
	    static createFrom(source: any = {}) {
	        return new FileContentResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.content = source["content"];
	        this.message = source["message"];
	    }
	}
	export class FileEntry {
	    name: string;
	    is_dir: boolean;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new FileEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.is_dir = source["is_dir"];
	        this.size = source["size"];
	    }
	}
	export class FileListResult {
	    success: boolean;
	    files: FileEntry[];
	    message?: string;
	
	    static createFrom(source: any = {}) {
	        return new FileListResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.files = this.convertValues(source["files"], FileEntry);
	        this.message = source["message"];
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
	export class LoginResult {
	    username: string;
	    isAdmin: boolean;
	    token: string;
	
	    static createFrom(source: any = {}) {
	        return new LoginResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.username = source["username"];
	        this.isAdmin = source["isAdmin"];
	        this.token = source["token"];
	    }
	}

}

