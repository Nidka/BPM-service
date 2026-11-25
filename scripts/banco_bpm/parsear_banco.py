#!/usr/bin/env python3
"""Convierte el texto del banco oficial de preguntas BPM (pdftotext -layout) en JSON versionado.

Uso:
  pdftotext -layout Banco_100_preguntas_BPM_Bloom_5_modulos.pdf banco.txt
  python3 -I scripts/banco_bpm/parsear_banco.py banco.txt \
      internal/features/training/catalog/banco_bpm_v1.json [--version 1]

El script solo lee el texto y escribe el JSON; no accede a red ni a bases de datos.
La justificación se extrae de la tabla de claves (sección 4) y se guarda tal cual para
que la prueba compruebe que coincide con la alternativa marcada como clave.
"""
import json
import re
import sys

MODULOS = {
    "M01": ("Inocuidad alimentaria e higiene personal", "BPM-M01"),
    "M02": ("Manipulación segura de alimentos", "BPM-M02"),
    "M03": ("Higiene y saneamiento del establecimiento", "BPM-M03"),
    "M04": ("Servicio, transporte y distribución de alimentos", "BPM-M04"),
    "M05": ("Supervisión, registros y control de BPM", "BPM-M05"),
}
# Etiqueta corta de cada fuente para la retroalimentación («Fuente: NTS 142 (P01)»). El título completo
# y el enlace salen de la sección 7 del documento.
ETIQUETAS_FUENTES = {
    "P01": "NTS 142 (P01)", "P02": "R. M. 811-2023 (P02)", "P03": "D. S. 007-98-SA (P03)",
    "P04": "R. M. 591-2008 (P04)", "P05": "R. M. 461-2007 (P05)", "P06": "D. S. 031-2010-SA (P06)",
    "P07": "NTS 234 (P07)", "P08": "R. M. 936-2025 (P08)",
    "C01": "Codex CXC 1-1969 (C01)", "C02": "Codex CXC 39-1993 (C02)", "C03": "Codex CXC 53-2003 (C03)",
    "C04": "Codex CXC 80-2020 (C04)", "G01": "OMS, cinco claves (G01)",
    "I03": "Ficha de inspección I03", "I05": "Guía de observación I05",
}
NIVELES = {"Recordar", "Comprender", "Aplicar", "Analizar", "Evaluar"}
RE_CODIGO = re.compile(r"^(M0[1-5]-\d{2})\s")
RE_ITEM = re.compile(r"^(M0[1-5]-\d{2}) \[(\w+)\] (.*)$")
RE_OPCION = re.compile(r"^([A-E])\. (.*)$")


ENCABEZADO = "PROYECTO BPM DIGITAL | INSTRUMENTO DE CAPACITACIÓN"


def limpiar_linea(l):
    """Quita el salto de página y el encabezado, que pdftotext puede dejar pegado al texto de la tabla."""
    l = l.replace("\f", "").replace(ENCABEZADO, "")
    return re.sub(r"\s*Banco de preguntas · Versión para validación \| Página \d+", "", l)


def norm(s):
    return re.sub(r"\s+", " ", s).strip()


def seccion(lineas, ini, fin):
    i = next(k for k, l in enumerate(lineas) if l.startswith(ini))
    j = next((k for k, l in enumerate(lineas) if k > i and l.startswith(fin)), len(lineas))
    return lineas[i:j]


def parsear_preguntas(lineas):
    bloque = seccion(lineas, "3. Banco completo de preguntas", "4. Claves de respuestas")
    items, actual, modo = {}, None, None
    for l in bloque:
        s = l.strip()
        if not s:
            continue
        m = RE_ITEM.match(s)
        if m:
            actual = {"code": m.group(1), "bloom": m.group(2), "text": m.group(3), "options": {}, "sources": []}
            items[actual["code"]] = actual
            modo, ultima = "texto", None
            continue
        if actual is None or s.startswith("MÓDULO") :
            if s.startswith("MÓDULO"):
                actual = None
            continue
        if s.startswith("Fuente base:"):
            actual["sources"] = [x for x in s.split(":", 1)[1].strip().split("/") if x]
            modo = "fin"
            continue
        m = RE_OPCION.match(s)
        if m and modo in ("texto", "opciones"):
            actual["options"][m.group(1)] = m.group(2)
            modo, ultima = "opciones", m.group(1)
            continue
        if modo == "texto":
            actual["text"] += " " + s
        elif modo == "opciones":
            actual["options"][ultima] += " " + s
        elif modo == "fin" and not s.startswith("Referencias") and "preguntas ·" not in s:
            pass
    for it in items.values():
        it["text"] = norm(it["text"])
        it["options"] = {k: norm(v) for k, v in it["options"].items()}
    return items


def parsear_claves(lineas):
    """La tabla de claves centra verticalmente el texto de cada fila alrededor de la línea del
    código. Cada justificación es una oración que termina en punto, así que se reparte cada
    línea de texto a la fila cuya oración aún no ha terminado."""
    bloque = seccion(lineas, "4. Claves de respuestas", "5. Matriz de correspondencia")
    filas = []  # (indice de línea, código, clave, texto en la misma línea, fuente)
    texto = []  # (índice, texto de columna de justificación) para líneas sin código
    registros = []
    for l in bloque:
        if not l.strip() or l.startswith("4. Claves") or l.startswith("Sección reservada") or l.startswith("Módulo ") or l.startswith("Código"):
            continue
        if "Banco de preguntas · Versión" in l or l.startswith("PROYECTO BPM DIGITAL") or "evaluaciones." == l.strip():
            continue
        m = re.match(r"^(M0[1-5]-\d{2})\s+([A-E])\s*(.*)$", l)
        if m:
            resto = m.group(3).rstrip()
            partes = re.split(r"\s{3,}", resto) if resto else []
            fuente = ""
            cuerpo = ""
            if partes:
                if re.fullmatch(r"[A-Z]\d{2}(/[A-Z]\d{2})*", partes[-1]):
                    fuente = partes[-1]
                    cuerpo = " ".join(partes[:-1])
                else:
                    cuerpo = " ".join(partes)
            registros.append({"tipo": "codigo", "codigo": m.group(1), "clave": m.group(2), "texto": cuerpo, "fuente": fuente})
        else:
            registros.append({"tipo": "texto", "texto": l.strip()})
    # asignar líneas de texto a filas: dentro de cada bloque entre códigos consecutivos, las
    # primeras líneas completan la fila anterior hasta cerrar la oración (punto final).
    codigos = [i for i, r in enumerate(registros) if r["tipo"] == "codigo"]
    resultado = {}
    for n, idx in enumerate(codigos):
        r = registros[idx]
        previo_ini = codigos[n - 1] if n > 0 else -1
        # líneas de texto justo antes de este código que pertenecen a esta fila
        antes = []
        j = idx - 1
        while j > previo_ini and registros[j]["tipo"] == "texto":
            antes.insert(0, registros[j]["texto"])
            j -= 1
        despues = []
        k = idx + 1
        sig = codigos[n + 1] if n + 1 < len(codigos) else len(registros)
        resultado[r["codigo"]] = {"antes": antes, "propio": r["texto"], "tramo": [x["texto"] for x in registros[idx + 1:sig]], "clave": r["clave"], "fuente": r["fuente"]}
    # Repartir cada tramo: entre el código n y n+1 hay líneas de la fila n (después) y de la
    # fila n+1 (antes). La fila n termina en la primera línea que acaba en punto.
    claves = {}
    codigos_ordenados = [registros[i]["codigo"] for i in codigos]
    pendiente_antes = {}
    for n, cod in enumerate(codigos_ordenados):
        info = resultado[cod]
        tramo_prev = []
        tramo = info["tramo"]
        cerrada = info["propio"].endswith(".")
        despues = []
        if not cerrada:
            while tramo:
                linea = tramo.pop(0)
                despues.append(linea)
                if linea.endswith("."):
                    break
        sobrante = tramo
        if n + 1 < len(codigos_ordenados):
            pendiente_antes[codigos_ordenados[n + 1]] = sobrante
        claves[cod] = {"clave": info["clave"], "fuente": info["fuente"], "despues": despues, "propio": info["propio"]}
    for n, cod in enumerate(codigos_ordenados):
        antes = pendiente_antes.get(cod, [])
        if n == 0:
            antes = resultado[cod]["antes"]
        c = claves[cod]
        c["justificacion"] = norm(" ".join(antes + ([c["propio"]] if c["propio"] else []) + c["despues"]))
    return claves


def parsear_fuentes(lineas):
    bloque = seccion(lineas, "7. Fuentes oficiales e instrumentos", "8. Bibliografía")
    texto = [l.strip() for l in bloque[1:] if l.strip() and "Banco de preguntas ·" not in l and not l.startswith("PROYECTO BPM DIGITAL")]
    fuentes, actual = {}, None
    for s in texto:
        m = re.match(r"^([PCGI]\d{2}) — (.*)$", s)
        if m:
            actual = {"code": m.group(1), "short": ETIQUETAS_FUENTES[m.group(1)], "title": m.group(2), "url": ""}
            fuentes[actual["code"]] = actual
        elif actual is not None:
            if s.startswith("http"):
                actual["url"] = s
            elif not actual["url"]:
                actual["title"] += " " + s
    for f in fuentes.values():
        f["title"] = norm(f["title"])
        f["url"] = norm(f["url"])
    return fuentes


def parsear_seleccion(lineas):
    bloque = seccion(lineas, "2. Evaluaciones breves recomendadas", "3. Banco completo")
    sel = {}
    for l in bloque:
        m = re.match(r"^Módulo (\d)\. .* → (.*)$", l.strip())
        if m:
            sel["M0" + m.group(1)] = [c.strip() for c in m.group(2).split(",")]
    return sel


def parsear_modulos(lineas):
    mods = {}
    for k, l in enumerate(lineas):
        m = re.match(r"^MÓDULO (\d)\. (.*)$", l.strip())
        if m:
            cod = "M0" + m.group(1)
            sub = lineas[k + 1].strip() if k + 1 < len(lineas) else ""
            mods[cod] = {"code": cod, "courseCode": MODULOS[cod][1], "title": norm(m.group(2)), "topics": sub}
    return mods


def main():
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    version = 1
    if "--version" in sys.argv:
        version = int(sys.argv[sys.argv.index("--version") + 1])
        args = [a for a in args if a != str(version)]
    if len(args) != 2:
        sys.exit(__doc__)
    with open(args[0], encoding="utf-8") as f:
        lineas = [limpiar_linea(l.rstrip("\n")) for l in f]
    items = parsear_preguntas(lineas)
    claves = parsear_claves(lineas)
    fuentes = parsear_fuentes(lineas)
    seleccion = parsear_seleccion(lineas)
    modulos = parsear_modulos(lineas)
    salida_items = []
    for cod in sorted(items):
        it = items[cod]
        c = claves[cod]
        mod = cod.split("-")[0]
        salida_items.append({
            "code": cod,
            "module": mod,
            "bloom": it["bloom"],
            "text": it["text"],
            "options": it["options"],
            "key": c["clave"],
            "justification": c["justificacion"],
            "sources": it["sources"],
        })
    out = {
        "version": version,
        "name": "Banco de 100 preguntas de capacitación en BPM (Bloom, 5 módulos)",
        "status": "borrador para validación (7 de noviembre de 2026)",
        "passScorePercent": 80,
        "itemsPerEvaluation": 5,
        "modules": [modulos[m] for m in sorted(modulos)],
        "recommendedSelection": seleccion,
        "sources": [fuentes[k] for k in sorted(fuentes)],
        "items": salida_items,
    }
    with open(args[1], "w", encoding="utf-8") as f:
        json.dump(out, f, ensure_ascii=False, indent=2)
        f.write("\n")
    print(f"{len(salida_items)} ítems, {len(fuentes)} fuentes")


if __name__ == "__main__":
    main()
