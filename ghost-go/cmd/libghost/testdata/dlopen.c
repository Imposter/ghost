/*
 * dlopen.c loads libghost the way a host application does (dlopen, as Dart's
 * DynamicLibrary.open), checks ghost_version, and makes one call that crosses
 * the boundary both ways. See docs/ffi.md, "Testing on Android".
 *
 *     dlopen /path/to/libghost.so
 *
 * Exit status 0 on success.
 */
#include <dlfcn.h>
#include <stdio.h>
#include <string.h>

typedef const char *(*version_fn)(void);
typedef char *(*enroll_fn)(const char *);
typedef void (*free_fn)(char *);

int main(int argc, char **argv) {
    const char *path = argc > 1 ? argv[1] : "libghost.so";
    void *lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    if (!lib) {
        fprintf(stderr, "dlopen %s: %s\n", path, dlerror());
        return 1;
    }
    version_fn version = (version_fn)dlsym(lib, "ghost_version");
    enroll_fn enroll = (enroll_fn)dlsym(lib, "ghost_enroll");
    free_fn release = (free_fn)dlsym(lib, "ghost_free");
    if (!version || !enroll || !release) {
        fprintf(stderr, "dlsym: %s\n", dlerror());
        return 1;
    }
    printf("ghost_version: %s\n", version());

    /* No server: refused before any network, with an error document. */
    char *doc = enroll("{}");
    printf("ghost_enroll({}): %s\n", doc);
    int ok = doc && strstr(doc, "\"ok\":false") != NULL;
    release(doc);
    return ok ? 0 : 1;
}
