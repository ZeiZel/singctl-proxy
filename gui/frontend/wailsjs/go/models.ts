export namespace bridge {
	
	export class Key {
	    index: number;
	    name: string;
	    masked: string;
	
	    static createFrom(source: any = {}) {
	        return new Key(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.name = source["name"];
	        this.masked = source["masked"];
	    }
	}
	export class ProcInfo {
	    PID: number;
	    Name: string;
	    Ports: string;
	    Children: number;
	
	    static createFrom(source: any = {}) {
	        return new ProcInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.PID = source["PID"];
	        this.Name = source["Name"];
	        this.Ports = source["Ports"];
	        this.Children = source["Children"];
	    }
	}
	export class Settings {
	    SocksPort: number;
	    ClashEnabled: boolean;
	    ClashAddr: string;
	    URLTestURL: string;
	    URLTestInterval: string;
	    URLTestTolerance: number;
	    SaveProfile: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SocksPort = source["SocksPort"];
	        this.ClashEnabled = source["ClashEnabled"];
	        this.ClashAddr = source["ClashAddr"];
	        this.URLTestURL = source["URLTestURL"];
	        this.URLTestInterval = source["URLTestInterval"];
	        this.URLTestTolerance = source["URLTestTolerance"];
	        this.SaveProfile = source["SaveProfile"];
	    }
	}
	export class Status {
	    running: boolean;
	    pid: number;
	    mode: string;
	    startedAt: string;
	    clashApi: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.pid = source["pid"];
	        this.mode = source["mode"];
	        this.startedAt = source["startedAt"];
	        this.clashApi = source["clashApi"];
	    }
	}

}

