let csrfToken = "";

export function setCSRF(val){
    csrfToken = val || "";
}

export async function api(path, {method = "GET", form, json, signal} ={}){
    const headers = {Accept : "application/json"};
    const option = {
        method, 
        headers, 
        signal,
        credentials: "same-origin",
        cache: "no-store",   
    };
    if (method!== "GET" && csrfToken){
        headers["X-CSRF-Token"] = csrfToken;
    }
    
    if (form !== undefined){
        option.body = new URLSearchParams(form)
    } else if (json !== undefined){
        headers["Content-Type"] = "application/json";
        option.body = JSON.stringify(json);
    }

    const response = await fetch("/api" + path, option);
    const raw = await response.text();

    let data; 
    try {
        data = raw ? JSON.parse(raw) : {};
    } catch{
        if (response.ok){
            throw new Error(`Invalid JSON: ${raw}`);
        }
        data = { error: raw.trim() || `HTTP ${response.status}` };
    }

    if (!response.ok || data.error){
        const error = new Error(data.error || data.message || `HTTP ${response.status}`);
        error.status = response.status;
        throw error;
    }
    return data; 
}