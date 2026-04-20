export namespace main {
	
	export class uiSelection {
	    tenant: string;
	    environment: string;
	
	    static createFrom(source: any = {}) {
	        return new uiSelection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tenant = source["tenant"];
	        this.environment = source["environment"];
	    }
	}
	export class startSessionResult {
	    sessionId: number;
	    selection: uiSelection;
	
	    static createFrom(source: any = {}) {
	        return new startSessionResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.selection = this.convertValues(source["selection"], uiSelection);
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
	export class uiBuildDetails {
	    version: string;
	    commit?: string;
	    date?: string;
	
	    static createFrom(source: any = {}) {
	        return new uiBuildDetails(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.commit = source["commit"];
	        this.date = source["date"];
	    }
	}
	export class uiEnvironment {
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new uiEnvironment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	    }
	}
	
	export class uiTenant {
	    name: string;
	    environments: uiEnvironment[];
	
	    static createFrom(source: any = {}) {
	        return new uiTenant(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.environments = this.convertValues(source["environments"], uiEnvironment);
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
	export class uiState {
	    tenants: uiTenant[];
	    selected?: uiSelection;
	    message?: string;
	    build: uiBuildDetails;
	
	    static createFrom(source: any = {}) {
	        return new uiState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tenants = this.convertValues(source["tenants"], uiTenant);
	        this.selected = this.convertValues(source["selected"], uiSelection);
	        this.message = source["message"];
	        this.build = this.convertValues(source["build"], uiBuildDetails);
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

